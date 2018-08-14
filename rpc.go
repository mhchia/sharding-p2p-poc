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

func callRPCAddPeer(rpcAddr string, ipAddr string, port int, seed int64) {
	conn, err := grpc.Dial(rpcAddr, grpc.WithInsecure())
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()
	client := pbrpc.NewPocClient(conn)
	addPeerReq := &pbrpc.RPCAddPeerReq{
		Ip:   ipAddr,
		Port: int32(port),
		Seed: seed,
	}
	log.Printf("rpcclient:AddPeer: sending=%v", addPeerReq)
	res, err := client.AddPeer(context.Background(), addPeerReq)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("rpcclient:AddPeer: result=%v", res)
}

func callRPCSubscribeShard(rpcAddr string, shardIDs []ShardIDType) {
	conn, err := grpc.Dial(rpcAddr, grpc.WithInsecure())
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()
	client := pbrpc.NewPocClient(conn)
	subscribeShardReq := &pbrpc.RPCSubscribeShardReq{
		ShardIDs: shardIDs,
	}
	log.Printf("rpcclient:ShardReq: sending=%v", subscribeShardReq)
	res, err := client.SubscribeShard(context.Background(), subscribeShardReq)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("rpcclient:ShardReq: result=%v", res)
}

func callRPCUnsubscribeShard(rpcAddr string, shardIDs []ShardIDType) {
	conn, err := grpc.Dial(rpcAddr, grpc.WithInsecure())
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()
	client := pbrpc.NewPocClient(conn)
	unsubscribeShardReq := &pbrpc.RPCUnsubscribeShardReq{
		ShardIDs: shardIDs,
	}
	log.Printf("rpcclient:UnsubscribeShardReq: sending=%v", unsubscribeShardReq)
	res, err := client.UnsubscribeShard(context.Background(), unsubscribeShardReq)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("rpcclient:UnsubscribeShardReq: result=%v", res)
}

func callRPCGetSubscribedShard(rpcAddr string) {
	conn, err := grpc.Dial(rpcAddr, grpc.WithInsecure())
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()
	client := pbrpc.NewPocClient(conn)
	getSubscribedShardReq := &pbrpc.RPCGetSubscribedShardReq{}
	log.Printf("rpcclient:GetSubscribedShard: sending=%v", getSubscribedShardReq)
	res, err := client.GetSubscribedShard(context.Background(), getSubscribedShardReq)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("rpcclient:GetSubscribedShard: result=%v", res.ShardIDs)
}

func callRPCBroadcastCollation(
	rpcAddr string,
	shardID ShardIDType,
	numCollations int,
	collationSize int,
	period int) {
	conn, err := grpc.Dial(rpcAddr, grpc.WithInsecure())
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()
	client := pbrpc.NewPocClient(conn)
	broadcastCollationReq := &pbrpc.RPCBroadcastCollationReq{
		ShardID: shardID,
		Number:  int32(numCollations),
		Size:    int32(collationSize),
		Period:  int32(period),
	}
	log.Printf("rpcclient:BroadcastCollation: sending=%v", broadcastCollationReq)
	res, err := client.BroadcastCollation(context.Background(), broadcastCollationReq)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("rpcclient:BroadcastCollation: result=%v", res)
}

func callRPCStopServer(rpcAddr string) {
	conn, err := grpc.Dial(rpcAddr, grpc.WithInsecure())
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()
	client := pbrpc.NewPocClient(conn)
	stopServerReq := &pbrpc.RPCStopServerReq{}
	log.Printf("rpcclient:StopServerReq: sending=%v", stopServerReq)
	res, err := client.StopServer(context.Background(), stopServerReq)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(res)
}

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
	_, targetPID, err := makeKey(req.Seed)
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
			int64(i),
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
