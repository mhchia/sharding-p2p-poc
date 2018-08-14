package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	pbrpc "github.com/ethresearch/sharding-p2p-poc/pb/rpc"
	"google.golang.org/grpc"

	opentracing "github.com/opentracing/opentracing-go"
)

type server struct {
	pbrpc.PocServer
	node       *Node
	parentSpan opentracing.Span
	rpcServer  *grpc.Server
}

func (s *server) AddPeer(ctx context.Context, req *pbrpc.RPCAddPeerReq) (*pbrpc.RPCReply, error) {
	// Add span for AddPeer
	span := opentracing.StartSpan("AddPeer", opentracing.ChildOf(s.parentSpan.Context()))
	defer span.Finish()

	log.Printf("rpcserver:AddPeer: receive=%v", req)
	_, targetPID, err := makeKey(int(req.Seed))
	mAddr := fmt.Sprintf(
		"/ip4/%s/tcp/%d/ipfs/%s",
		req.Ip,
		req.Port,
		targetPID.Pretty(),
	)
	if err != nil {
		log.Fatal(err)
	}

	var replyMsg string
	var status bool
	if success := s.node.AddPeer(mAddr); success {
		replyMsg = fmt.Sprintf("Added Peer %v:%v, pid=%v!", req.Ip, req.Port, targetPID)
		status = true
		// Tag the span with peer info
		span.SetTag("Peer info", fmt.Sprintf("%v:%v", req.Ip, req.Port))
	} else {
		replyMsg = fmt.Sprintf("Failed to add Peer %v:%v, pid=%v!", req.Ip, req.Port, targetPID)
		status = false
	}
	span.SetTag("Status", status)
	res := &pbrpc.RPCReply{
		Message: replyMsg,
		Status:  status,
	}
	return res, nil
}

func (s *server) SubscribeShard(
	ctx context.Context,
	req *pbrpc.RPCSubscribeShardReq) (*pbrpc.RPCReply, error) {
	// Add span for SubscribeShard
	span := opentracing.StartSpan("SubscribeShard", opentracing.ChildOf(s.parentSpan.Context()))
	defer span.Finish()

	log.Printf("rpcserver:SubscribeShardReq: receive=%v", req)
	for _, shardID := range req.ShardIDs {
		s.node.ListenShard(ctx, shardID)
		time.Sleep(time.Millisecond * 30)
	}
	s.node.PublishListeningShards()
	res := &pbrpc.RPCReply{
		Message: fmt.Sprintf(
			"Subscribed shard %v",
			req.ShardIDs,
		),
		Status: true,
	}
	// Tag the span with shard info
	span.SetTag("Shard info", fmt.Sprintf("shard %v", req.ShardIDs))
	return res, nil
}

func (s *server) UnsubscribeShard(
	ctx context.Context,
	req *pbrpc.RPCUnsubscribeShardReq) (*pbrpc.RPCReply, error) {
	// Add span for UnsubscribeShard
	span := opentracing.StartSpan("UnsubscribeShard", opentracing.ChildOf(s.parentSpan.Context()))
	defer span.Finish()

	log.Printf("rpcserver:UnsubscribeShardReq: receive=%v", req)
	for _, shardID := range req.ShardIDs {
		s.node.UnlistenShard(shardID)
		time.Sleep(time.Millisecond * 30)
	}
	s.node.PublishListeningShards()
	res := &pbrpc.RPCReply{
		Message: fmt.Sprintf(
			"Unsubscribed shard %v",
			req.ShardIDs,
		),
		Status: true,
	}
	// Tag the span with shard info
	span.SetTag("Shard info", fmt.Sprintf("shard %v", req.ShardIDs))
	return res, nil
}

func (s *server) GetSubscribedShard(
	ctx context.Context,
	req *pbrpc.RPCGetSubscribedShardReq) (*pbrpc.RPCGetSubscribedShardReply, error) {
	// Add span for GetSubscribedShard
	span := opentracing.StartSpan("GetSubscribedShard", opentracing.ChildOf(s.parentSpan.Context()))
	defer span.Finish()

	log.Printf("rpcserver:GetSubscribedShard: receive=%v", req)
	shardIDs := s.node.GetListeningShards()
	res := &pbrpc.RPCGetSubscribedShardReply{
		ShardIDs: shardIDs,
		Status:   true,
	}
	// Tag the span with shards info
	span.SetTag("Shards subscribed to", shardIDs)
	return res, nil
}

func (s *server) BroadcastCollation(
	ctx context.Context,
	req *pbrpc.RPCBroadcastCollationReq) (*pbrpc.RPCReply, error) {
	// Add span for BroadcastCollation
	span := opentracing.StartSpan("BroadcastCollation", opentracing.ChildOf(s.parentSpan.Context()))
	defer span.Finish()

	log.Printf("rpcserver:BroadcastCollationReq: receive=%v", req)
	shardID := req.ShardID
	numCollations := int(req.Number)
	timeInMs := req.Period
	sizeInBytes := req.Size
	if sizeInBytes > 100 {
		sizeInBytes -= 100
	}
	for i := 0; i < numCollations; i++ {
		// control the speed of sending collations
		time.Sleep(time.Millisecond * time.Duration(timeInMs))
		randBytes := make([]byte, sizeInBytes)
		rand.Read(randBytes)
		// TODO: catching error
		s.node.broadcastCollation(
			ShardIDType(shardID),
			i,
			randBytes,
		)
	}
	res := &pbrpc.RPCReply{
		Message: fmt.Sprintf(
			"Finished sending %v size=%v collations in shard %v",
			numCollations,
			sizeInBytes,
			shardID,
		),
		Status: true,
	}
	// Tag the span with collations info
	span.SetTag("Number of collations", numCollations)
	span.SetTag("Size of collation", sizeInBytes)
	span.SetTag("Shard", shardID)
	return res, nil
}

func (s *server) StopServer(
	ctx context.Context,
	req *pbrpc.RPCStopServerReq) (*pbrpc.RPCReply, error) {
	// Add span for StopServer
	span := opentracing.StartSpan("StopServer", opentracing.ChildOf(s.parentSpan.Context()))

	log.Printf("rpcserver:StopServer: receive=%v", req)
	time.Sleep(time.Millisecond * 500)
	span.Finish()
	log.Printf("Closing RPC server by rpc call...")
	s.rpcServer.Stop()

	res := &pbrpc.RPCReply{
		Message: fmt.Sprintf("Closed RPC server"),
		Status:  true,
	}
	return res, nil
}

func runRPCServer(n *Node, addr string) {
	// Start a new trace
	span := opentracing.StartSpan("RPC Server")
	span.SetTag("Seed Number", n.number)

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal(err)
	}
	s := grpc.NewServer()
	pbrpc.RegisterPocServer(s, &server{node: n, parentSpan: span, rpcServer: s})

	// Catch interupt signal
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		log.Printf("Closing RPC server by Interrupt signal...")
		s.Stop()
	}()

	log.Printf("rpcserver: listening to %v", addr)
	s.Serve(lis)
	span.Finish()
}
