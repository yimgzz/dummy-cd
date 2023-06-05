package server

import (
	"github.com/yimgzz/dummy-cd/server/pkg/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Client struct {
	pb.DummycdClient
}

func NewServerClient(serverAddress string) (*Client, error) {
	conn, err := grpc.Dial(serverAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))

	if err != nil {
		return nil, err
	}

	client := &Client{pb.NewDummycdClient(conn)}

	return client, nil
}
