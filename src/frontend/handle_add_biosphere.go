package main

import (
	"./api"
	"errors"
	"github.com/golang/protobuf/proto"
	"golang.org/x/net/context"
	"google.golang.org/cloud/datastore"
	"io/ioutil"
	"log"
)

const InitialEnvBucket = "bonsai_initial_envs"

func (fe *FeServiceImpl) AddBiosphere(ctx context.Context, q *api.AddBiosphereQ) (*api.AddBiosphereS, error) {
	ctx = TraceStart(ctx, "/frontend.AddBiosphere")
	defer TraceEnd(ctx, fe.ServerCred)

	if q.Auth == nil {
		return nil, errors.New("AddBiosphere requires auth")
	}
	canWrite, err := fe.isWriteAuthorized(ctx, q.Auth)
	if err != nil {
		return nil, err
	}
	if !canWrite {
		return nil, errors.New("UI must disallow unauthorized actions")
	}

	client, err := fe.AuthDatastore(ctx)
	if err != nil {
		return nil, err
	}

	valid, err := fe.isValidNewConfig(ctx, client, q.Config)
	if err != nil {
		return nil, err
	}
	if !valid || q.TestOnly {
		return &api.AddBiosphereS{Success: valid}, nil
	}

	if q.Config.Env.StorageFileId != "" {
		fe.getStorage(ctx, q.Config.Env.StorageFileId)
	}

	envBlob, err := proto.Marshal(q.Config.Env)
	if err != nil {
		return nil, err
	}
	key := datastore.NewIncompleteKey(ctx, "BiosphereMeta", nil)
	meta := &BiosphereMeta{
		Name: q.Config.Name,
		Nx:   q.Config.Nx,
		Ny:   q.Config.Ny,
		Env:  envBlob,
	}
	key, err = client.Put(ctx, key, meta)
	if err != nil {
		return nil, err
	}

	return &api.AddBiosphereS{
		Success: true,
		BiosphereDesc: &api.BiosphereDesc{
			BiosphereId: uint64(key.ID()),
			Name:        meta.Name,
			NumCores:    uint32(meta.Nx*meta.Ny/5) + 1,
		},
	}, nil
}

func (fe *FeServiceImpl) getStorage(ctx context.Context, objectName string) {
	ctx = TraceStart(ctx, "/frontend._.getStorage")
	defer TraceEnd(ctx, fe.ServerCred)

	service, err := fe.AuthStorage(ctx)

	res, err := service.Objects.Get(InitialEnvBucket, objectName).Download()
	if err != nil {
		// TODO: should report error to user
		log.Printf("Failed to retrieve cloud storage object containing initial env, %v", err)
		return
	}
	defer res.Body.Close()
	blob, err := ioutil.ReadAll(res.Body)
	if err != nil {
		log.Printf("ERROR: Failed to download storage %v", err)
		return
	}
	snapshot := &api.ChunkSnapshot{}
	err = proto.Unmarshal(blob, snapshot)
	if err != nil {
		// TODO: should report error to user
		log.Printf("ERROR: Failed to unmarshal snapshot proto %v", err)
		return
	}
}

func (fe *FeServiceImpl) isValidNewConfig(ctx context.Context, dsClient *datastore.Client, config *api.BiosphereCreationConfig) (bool, error) {
	if config == nil {
		return false, nil
	}
	if config.Name == "" || config.Nx <= 0 || config.Ny <= 0 {
		return false, nil
	}
	// Name must be unique.
	qSameName := datastore.NewQuery("BiosphereMeta").Filter("Name =", config.Name)
	numSameName, err := dsClient.Count(ctx, qSameName)
	if err != nil {
		return false, err
	}
	if numSameName > 0 {
		return false, nil
	}
	return true, nil
}
