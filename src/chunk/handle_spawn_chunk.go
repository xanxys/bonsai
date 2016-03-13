package main

import (
	"./api"
	"errors"
	"fmt"
	"github.com/golang/protobuf/proto"
	"golang.org/x/net/context"
	"google.golang.org/cloud/datastore"
	"log"
)

func (ck *CkServiceImpl) SpawnChunk(ctx context.Context, q *api.SpawnChunkQ) (*api.SpawnChunkS, error) {
	go RunChunk(ck.ChunkRouter, q, ck.ServerCred)
	return &api.SpawnChunkS{}, nil
}

func RunChunk(router *ChunkRouter, q *api.SpawnChunkQ, cred *ServerCred) {
	ctx := context.Background()
	topo := q.Topology
	relToId, idToRel, wall := decodeTopo(topo)

	var chunk *GrainChunk
	if q.StartTimestamp > 0 || q.InitFromSnapshot {
		loadedChunk, err := resumeFromSnapshot(ctx, topo.ChunkId, q.StartTimestamp, cred)
		if err != nil {
			log.Printf("Resuming failed with %#v, not starting %s", err, topo.ChunkId)
			return
		}
		chunk = loadedChunk
	} else {
		chunk = initializeWithSources(q)
	}

	quitCh := router.RegisterNewChunk(topo)
	if quitCh == nil {
		log.Printf("RunChunk(%s) exiting because it's already running", topo.ChunkId)
		return
	}
	// Post initial empty state to unblock other chunks.
	grains := make([]*api.Grain, len(chunk.Grains))
	for ix, grain := range chunk.Grains {
		grains[ix] = ser(grain)
	}
	router.NotifyResult(chunk.Timestamp, topo, &NeighborExport{
		ChunkGrains:   grains,
		EscapedGrains: make(map[string][]*api.Grain),
	})

	for {
		select {
		case <-quitCh:
			log.Printf("Quit signal received")
			break
		case nImport := <-router.RequestNeighbor(chunk.Timestamp, topo):
			// Unpack imported things and import.
			incomingGrains := make([]*Grain, len(nImport.IncomingGrains))
			for ix, grainProto := range nImport.IncomingGrains {
				incomingGrains[ix] = deser(grainProto)
			}
			var envGrains []*Grain
			for chunkId, sGrains := range nImport.EnvGrains {
				rel := idToRel[chunkId]
				deltaPos := Vec3f{float32(rel.Dx), float32(rel.Dy), 0}
				for _, grainProto := range sGrains {
					grain := deser(grainProto)
					grain.Position = grain.Position.Add(deltaPos)
					envGrains = append(envGrains)
				}
			}
			chunk.IncorporateAddition(incomingGrains)

			// Persist when requested.
			if q.SnapshotModulo > 0 && chunk.Timestamp%uint64(q.SnapshotModulo) == 0 {
				key, err := takeSnapshot(ctx, q.Topology.ChunkId, cred, chunk)
				if err != nil {
					log.Printf("Error: Failed to take snapshot with %#v", err)
				}
				log.Printf("Snapshot key=%v", key)
			}

			// Actual simulation.
			escapedGrains := chunk.Step(envGrains, wall)

			// Pack exported things.
			grains := make([]*api.Grain, len(chunk.Grains))
			for ix, grain := range chunk.Grains {
				grains[ix] = ser(grain)
			}
			bins := make(map[string][]*api.Grain)
			for _, escapedGrain := range escapedGrains {
				coord := binExternal(relToId, escapedGrain.Position)
				if coord == nil {
					continue
				}
				sGrain := ser(escapedGrain)
				sGrain.Pos = &api.CkPosition{
					coord.Pos.X, coord.Pos.Y, coord.Pos.Z,
				}
				bins[coord.Key] = append(bins[coord.Key], sGrain)
			}
			nExport := &NeighborExport{
				ChunkGrains:   grains,
				EscapedGrains: bins,
			}
			router.NotifyResult(chunk.Timestamp, topo, nExport)
		}
	}
}

func resumeFromSnapshot(ctx context.Context, chunkId string, startTimestamp uint64, cred *ServerCred) (*GrainChunk, error) {
	client, err := cred.AuthDatastore(ctx)
	if err != nil {
		return nil, err
	}

	// Find resuming point and delete snapshots after it.
	// This is super inefficient.
	qSnapshots := datastore.NewQuery("PersistentChunkSnapshot").Filter("ChunkId=", chunkId)
	var ss []*PersistentChunkSnapshot
	keys, err := client.GetAll(ctx, qSnapshots, &ss)
	if err != nil {
		return nil, err
	}
	var keysToDelete []*datastore.Key
	var resumePoint *PersistentChunkSnapshot
	for ix, snapshot := range ss {
		if uint64(snapshot.Timestamp) == startTimestamp {
			resumePoint = snapshot
		} else if uint64(snapshot.Timestamp) > startTimestamp {
			keysToDelete = append(keysToDelete, keys[ix])
		}
	}
	if resumePoint == nil {
		return nil, errors.New(fmt.Sprintf("PersistentChunkSnapshot(id=%s, t=%d) not found", chunkId, startTimestamp))
	}

	// Initialize chunk from snapshot.
	snapshotProto := &api.ChunkSnapshot{}
	err = proto.Unmarshal(resumePoint.Snapshot, snapshotProto)
	if err != nil {
		return nil, err
	}
	chunk := NewGrainChunk(false)
	chunk.Timestamp = startTimestamp
	chunk.Grains = make([]*Grain, len(snapshotProto.Grains))
	for ix, grainProto := range snapshotProto.Grains {
		chunk.Grains[ix] = deser(grainProto)
	}

	// Only after confirming successful chunk resuming, delete snapshots after resume point.
	err = client.DeleteMulti(ctx, keysToDelete)
	if err != nil {
		log.Printf("Error: Failed to delete %d snapshots when resuming from t=%d: %#v", len(keysToDelete), startTimestamp, keysToDelete)
	}
	return chunk, nil
}

func initializeWithSources(q *api.SpawnChunkQ) *GrainChunk {
	chunk := NewGrainChunk(false)
	if q.NumSoil > 0 {
		chunk.Sources = append(chunk.Sources, NewParticleSource(api.Grain_SOIL, int(q.NumSoil), Vec3f{0.5, 0.5, 2.0}))
	}
	if q.NumWater > 0 {
		chunk.Sources = append(chunk.Sources, NewParticleSource(api.Grain_WATER, int(q.NumWater), Vec3f{0.5, 0.55, 2.1}))
	}
	chunk.Sources = append(chunk.Sources, NewParticleSource(api.Grain_CELL, int(10), Vec3f{0.55, 0.5, 2.2}))
	return chunk
}

func decodeTopo(topo *api.ChunkTopology) (map[ChunkRel]string, map[string]ChunkRel, *ChunkWall) {
	relToId := make(map[ChunkRel]string)
	idToRel := make(map[string]ChunkRel)
	for _, neighbor := range topo.Neighbors {
		rel := ChunkRel{int(neighbor.Dx), int(neighbor.Dy)}
		relToId[rel] = neighbor.ChunkId
		idToRel[neighbor.ChunkId] = rel
	}
	_, canPassXm := relToId[ChunkRel{-1, 0}]
	_, canPassXp := relToId[ChunkRel{1, 0}]
	_, canPassYm := relToId[ChunkRel{0, -1}]
	_, canPassYp := relToId[ChunkRel{0, 1}]
	wall := &ChunkWall{
		Xm: !canPassXm,
		Xp: !canPassXp,
		Ym: !canPassYm,
		Yp: !canPassYp,
	}
	return relToId, idToRel, wall
}

func takeSnapshot(ctx context.Context, chunkId string, cred *ServerCred, chunk *GrainChunk) (*datastore.Key, error) {
	client, err := cred.AuthDatastore(ctx)
	if err != nil {
		return nil, err
	}

	grains := make([]*api.Grain, len(chunk.Grains))
	for ix, grain := range chunk.Grains {
		grains[ix] = ser(grain)
	}
	ssBlob, err := proto.Marshal(&api.ChunkSnapshot{
		Grains: grains,
	})
	if err != nil {
		return nil, err
	}

	log.Printf("Snapshotting at t=%d size=%d", chunk.Timestamp, len(ssBlob))
	key := datastore.NewIncompleteKey(ctx, "PersistentChunkSnapshot", nil)
	key, err = client.Put(ctx, key, &PersistentChunkSnapshot{
		ChunkId:   chunkId,
		Timestamp: int64(chunk.Timestamp),
		Snapshot:  ssBlob,
	})
	if err != nil {
		return nil, err
	}
	return key, nil
}

type ChunkRel struct {
	Dx, Dy int
}

type WorldCoord2 struct {
	Key string
	Pos Vec3f
}

// Convert a known-to-be-outlying point to WorldCoord.
func binExternal(relToId map[ChunkRel]string, pos Vec3f) *WorldCoord2 {
	ix := ifloor(pos.X)
	iy := ifloor(pos.Y)
	if ix == 0 && iy == 0 {
		log.Printf("Pos declared ougoing, but found in-chunk: %#v", pos)
		return nil
	}

	key, ok := relToId[ChunkRel{ix, iy}]
	if ok {
		return &WorldCoord2{key, pos.Sub(Vec3f{float32(ix), float32(iy), 0})}
	} else {
		log.Printf("Grain (pos %v) escaped to walled region, returning (0.5, 0.5, 10)", pos)
		return nil
	}
}

func deser(grain *api.Grain) *Grain {
	p := grain.Pos
	v := grain.Vel
	return &Grain{
		Id:       grain.Id,
		Position: Vec3f{p.X, p.Y, p.Z},
		Velocity: Vec3f{v.X, v.Y, v.Z},
		Kind:     grain.Kind,
		CellProp: grain.CellProp,
	}
}

func ser(grain *Grain) *api.Grain {
	p := grain.Position
	v := grain.Velocity
	return &api.Grain{
		Id:       grain.Id,
		Pos:      &api.CkPosition{p.X, p.Y, p.Z},
		Vel:      &api.CkVelocity{v.X, v.Y, v.Z},
		Kind:     grain.Kind,
		CellProp: grain.CellProp,
	}
}

func ifloor(x float32) int {
	if x >= 0 {
		return int(x)
	} else {
		return int(x) - 1
	}
}

func iabs(x int) int {
	if x >= 0 {
		return x
	} else {
		return -x
	}
}
