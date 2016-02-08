package main

import (
	"./api"
	"golang.org/x/net/context"
	"google.golang.org/cloud/datastore"
)

const tickPerYear = 5000

func (fe *FeServiceImpl) Biospheres(ctx context.Context, q *api.BiospheresQ) (*api.BiospheresS, error) {
	stateReceiver := make(chan map[uint64]api.BiosphereState, 1)
	fe.cmdQueue <- &ControllerCommand{getBiosphereStates: stateReceiver}

	client, err := fe.AuthDatastore(ctx)
	if err != nil {
		return nil, err
	}
	dq := datastore.NewQuery("BiosphereMeta")

	var metas []*BiosphereMeta
	keys, err := client.GetAll(ctx, dq, &metas)
	if err != nil {
		return nil, err
	}

	chunkState := <-stateReceiver
	var bios []*api.BiosphereDesc
	for ix, meta := range metas {
		state, ok := chunkState[uint64(keys[ix].ID())]
		if !ok {
			state = api.BiosphereState_STOPPED
		}
		topo := NewCylinderTopology(uint64(keys[ix].ID()), int(meta.Nx), int(meta.Ny))
		chunkId := topo.GetChunkTopos()[0].ChunkId
		query := datastore.NewQuery("PersistentChunkSnapshot").Filter("=ChunkId", chunkId).Project("Timestamp").Distinct().Order("Timestamp")
		var ss []*PersistentChunkSnapshot
		_, err := client.GetAll(ctx, query, &ss)
		if err != nil {
			return nil, err
		}
		maxTimestamp := uint64(ss[len(ss)-1].Timestamp)
		var persistedYears []int32
		for _, snapshot := range ss {
			if snapshot.Timestamp%tickPerYear == 0 {
				persistedYears = append(persistedYears, int32(snapshot.Timestamp/tickPerYear))
			}
		}
		bios = append(bios, &api.BiosphereDesc{
			BiosphereId:    uint64(keys[ix].ID()),
			Name:           meta.Name,
			NumCores:       uint32(meta.Nx*meta.Ny/5) + 1,
			NumTicks:       maxTimestamp,
			State:          state,
			Nx:             meta.Nx,
			Ny:             meta.Ny,
			PersistedYears: persistedYears,
		})
	}
	return &api.BiospheresS{
		Biospheres: bios,
	}, nil
}
