package main

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/golang/protobuf/proto"

	pbevent "github.com/ethresearch/sharding-p2p-poc/pb/event"
	pbmsg "github.com/ethresearch/sharding-p2p-poc/pb/message"
	pstore "github.com/libp2p/go-libp2p-peerstore"
	ma "github.com/multiformats/go-multiaddr"
	"google.golang.org/grpc"
	// gologging "github.com/whyrusleeping/go-logging"
)

var nodeCount int

func makeUnbootstrappedNode(t *testing.T, ctx context.Context, number int) *Node {
	return makeTestingNode(t, ctx, number, false, nil)
}

func makeTestingNode(
	t *testing.T,
	ctx context.Context,
	number int,
	doBootstrapping bool,
	bootstrapPeers []pstore.PeerInfo) *Node {
	// FIXME:
	//		1. Use 20000 to avoid conflitcs with the running nodes in the environment
	//		2. Avoid reuse of listeningPort in the entire test, to avoid `dial error`s
	listeningPort := 20000 + nodeCount
	nodeCount++
	node, err := makeNode(ctx, defaultIP, listeningPort, number, nil, doBootstrapping, bootstrapPeers)
	if err != nil {
		t.Error("Failed to create node")
	}
	return node
}

func waitForPubSubMeshBuilt() {
	time.Sleep(time.Second * 2)
}

func protoToBytes(t *testing.T, data proto.Message) []byte {
	bytes, err := proto.Marshal(data)
	if err != nil {
		t.Errorf("error occurred when marshaling proto: %v", err)
	}
	return bytes
}

func TestNodeListeningShards(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	node := makeUnbootstrappedNode(t, ctx, 0)
	var testingShardID ShardIDType = 42
	// test `IsShardListened`
	if node.IsShardListened(testingShardID) {
		t.Errorf("Shard %v haven't been listened", testingShardID)
	}
	// test `ListenShard`
	node.ListenShard(ctx, testingShardID)
	node.PublishListeningShards(ctx)
	if !node.IsShardListened(testingShardID) {
		t.Errorf("Shard %v should have been listened", testingShardID)
	}
	if !node.IsCollationSubscribed(testingShardID) {
		t.Errorf(
			"shardCollations in shard [%v] should be subscribed",
			testingShardID,
		)
	}
	anotherShardID := testingShardID + 1
	node.ListenShard(ctx, anotherShardID)
	node.PublishListeningShards(ctx)
	if !node.IsShardListened(anotherShardID) {
		t.Errorf("Shard %v should have been listened", anotherShardID)
	}
	shardIDs := node.GetListeningShards()
	if len(shardIDs) != 2 {
		t.Errorf("We should have 2 shards being listened, instead of %v", len(shardIDs))
	}
	// test `UnlistenShard`
	node.UnlistenShard(ctx, testingShardID)
	node.PublishListeningShards(ctx)
	if node.IsShardListened(testingShardID) {
		t.Errorf("Shard %v should have been unlistened", testingShardID)
	}
	if node.IsCollationSubscribed(testingShardID) {
		t.Errorf(
			"shardCollations in shard [%v] should be already unsubscribed",
			testingShardID,
		)
	}
	node.UnlistenShard(ctx, testingShardID) // ensure no side effect
	node.PublishListeningShards(ctx)
}

func connect(t *testing.T, ctx context.Context, a, b *Node) {
	err := a.AddPeer(ctx, b.GetFullAddr())
	if err != nil {
		t.Errorf("%v failed to `AddPeer` %v : %v", a.ID(), b.ID(), err)
	}
	if !a.IsPeer(b.ID()) || !b.IsPeer(a.ID()) {
		t.Error("AddPeer failed")
	}
	pinfo := pstore.PeerInfo{
		ID:    a.ID(),
		Addrs: a.Addrs(),
	}
	err = b.Connect(ctx, pinfo)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Network().ConnsToPeer(b.ID())) == 0 || len(b.Network().ConnsToPeer(a.ID())) == 0 {
		t.Errorf("failed to connect %v with %v", a.ID(), b.ID())
	}
	time.Sleep(time.Millisecond * 100)
}

func makeNodes(t *testing.T, ctx context.Context, number int) []*Node {
	nodes := []*Node{}
	for i := 0; i < number; i++ {
		nodes = append(nodes, makeUnbootstrappedNode(t, ctx, i))
	}
	time.Sleep(time.Millisecond * 100)
	return nodes
}

func connectBarbell(t *testing.T, ctx context.Context, nodes []*Node) {
	for i := 0; i < len(nodes)-1; i++ {
		connect(t, ctx, nodes[i], nodes[i+1])
	}
}

func connectAll(t *testing.T, ctx context.Context, nodes []*Node) {
	for i, nodei := range nodes {
		for j, nodej := range nodes {
			if i < j {
				connect(t, ctx, nodei, nodej)
			}
		}
	}
}

func TestAddPeer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	nodes := makeNodes(t, ctx, 2)
	connect(t, ctx, nodes[0], nodes[1])
}

func TestBroadcastCollation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nodes := makeNodes(t, ctx, 2)
	connectBarbell(t, ctx, nodes)

	var testingShardID ShardIDType = 42
	nodes[0].ListenShard(ctx, testingShardID)
	nodes[0].PublishListeningShards(ctx)
	// TODO: fail: if the receiver didn't subscribe the shard, it should ignore the message

	nodes[1].ListenShard(ctx, testingShardID)
	nodes[1].PublishListeningShards(ctx)
	// TODO: fail: if the collation's shardID does not correspond to the protocol's shardID,
	//		 receiver should reject it

	// success
	err := nodes[0].broadcastCollation(
		ctx,
		testingShardID,
		1,
		[]byte("123"),
	)
	if err != nil {
		t.Errorf("failed to send collation %v, %v, %v. reason=%v", testingShardID, 1, 123, err)
	}
}

func TestRequestShardPeer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	nodes := makeNodes(t, ctx, 2)
	connectBarbell(t, ctx, nodes)
	shardPeers, err := nodes[0].requestShardPeer(ctx, nodes[1].ID(), []ShardIDType{1})
	if err != nil {
		t.Errorf("Error occurred when requesting shard peer: %v", err)
	}
	peerIDs, prs := shardPeers[1]
	if !prs {
		t.Errorf("node1 should still return a empty peer list of the shard %v", 1)
	}
	if len(peerIDs) != 0 {
		t.Errorf("Wrong shard peer response %v, should be empty peerIDs", peerIDs)
	}
	nodes[1].ListenShard(ctx, 1)
	nodes[1].ListenShard(ctx, 2)
	// node1 listens to [1, 2], but we only request shard [1]
	shardPeers, err = nodes[0].requestShardPeer(ctx, nodes[1].ID(), []ShardIDType{1})
	if err != nil {
		t.Errorf("Error occurred when requesting shard peer: %v", err)
	}
	peerIDs1, prs := shardPeers[1]
	if len(peerIDs1) != 1 || peerIDs1[0] != nodes[1].ID() {
		t.Errorf("Wrong shard peer response %v, should be nodes[1].ID() only", peerIDs1)
	}
	// node1 only listens to [1, 2], but we request shard [1, 2, 3]
	shardPeers, err = nodes[0].requestShardPeer(ctx, nodes[1].ID(), []ShardIDType{1, 2, 3})
	if err != nil {
		t.Errorf("Error occurred when requesting shard peer: %v", err)
	}
	peerIDs1, prs = shardPeers[1]
	if len(peerIDs1) != 1 || peerIDs1[0] != nodes[1].ID() {
		t.Errorf("Wrong shard peer response %v, should be nodes[1].ID() only", peerIDs1)
	}
	peerIDs2, prs := shardPeers[2]
	if len(peerIDs2) != 1 || peerIDs2[0] != nodes[1].ID() {
		t.Errorf("Wrong shard peer response %v, should be nodes[1].ID() only", peerIDs2)
	}
	peerIDs3, prs := shardPeers[3]
	if !prs {
		t.Errorf("node1 should still return a empty peer list of the shard %v", 3)
	}
	if len(peerIDs3) != 0 {
		t.Errorf("Wrong shard peer response %v, should be empty peerIDs", peerIDs3)
	}
}

func TestRouting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// set the logger to DEBUG, to see the process of dht.FindPeer
	// we should be able to see something like
	// "dht: FindPeer <peer.ID d3wzD2> true routed.go:76", if successfully found the desire peer
	// golog.SetAllLoggers(gologging.DEBUG) // Change to DEBUG for extra info
	nodes := makeNodes(t, ctx, 3)
	connectBarbell(t, ctx, nodes)
	if nodes[0].IsPeer(nodes[2].ID()) {
		t.Error("node0 should not be able to reach node2 before routing")
	}
	nodes[0].Connect(
		ctx,
		pstore.PeerInfo{
			ID: nodes[2].ID(),
		},
	)
	time.Sleep(time.Millisecond * 100)
	if !nodes[2].IsPeer(nodes[0].ID()) {
		t.Error("node0 should be a peer of node2 now")
	}
}

func TestPubSub(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nodes := makeNodes(t, ctx, 3)
	connectBarbell(t, ctx, nodes)

	topic := "iamtopic"

	subch0, err := nodes[0].pubsubService.Subscribe(topic)
	if err != nil {
		t.Fatal(err)
	}

	// TODO: This sleep is necessary!!! Find out why
	time.Sleep(time.Millisecond * 100)

	publishMsg := "789"
	err = nodes[1].pubsubService.Publish(topic, []byte(publishMsg))
	if err != nil {
		t.Fatal(err)
	}
	msg0, err := subch0.Next(ctx)
	if string(msg0.Data) != publishMsg {
		t.Fatal(err)
	}
	if msg0.GetFrom() != nodes[1].ID() {
		t.Error("Wrong ID")
	}
}

func TestPubSubNotifyListeningShards(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nodes := makeNodes(t, ctx, 3)
	connectBarbell(t, ctx, nodes)

	waitForPubSubMeshBuilt()

	// ensure notifyShards message is propagated through node1
	if len(nodes[1].shardPrefTable.GetPeerListeningShardSlice(nodes[0].ID())) != 0 {
		t.Error()
	}
	nodes[0].ListenShard(ctx, 42)
	nodes[0].PublishListeningShards(ctx)
	time.Sleep(time.Millisecond * 100)
	if len(nodes[1].shardPrefTable.GetPeerListeningShardSlice(nodes[0].ID())) != 1 {
		t.Error()
	}
	if len(nodes[2].shardPrefTable.GetPeerListeningShardSlice(nodes[0].ID())) != 1 {
		t.Error()
	}
	nodes[1].ListenShard(ctx, 42)
	nodes[1].PublishListeningShards(ctx)

	time.Sleep(time.Millisecond * 100)

	shardPeers42 := nodes[2].shardPrefTable.GetPeersInShard(42)
	if len(shardPeers42) != 2 {
		t.Errorf(
			"len(shardPeers42) should be %v, not %v. shardPeers42=%v",
			2,
			len(shardPeers42),
			shardPeers42,
		)
	}

	// test unsetShard with notifying
	nodes[0].UnlistenShard(ctx, 42)
	nodes[0].PublishListeningShards(ctx)
	time.Sleep(time.Millisecond * 100)
	if len(nodes[1].shardPrefTable.GetPeerListeningShardSlice(nodes[0].ID())) != 0 {
		t.Error()
	}
}

// test if nodes can find each other with ipfs nodes
func TestWithIPFSNodesRouting(t *testing.T) {
	// FIXME: skipped by default, since currently our boostrapping nodes are local ipfs nodes
	t.Skip()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// golog.SetAllLoggers(gologging.DEBUG) // Change to DEBUG for extra info
	ipfsPeers := convertPeers([]string{
		"/ip4/127.0.0.1/tcp/4001/ipfs/QmXa1ncfGc9RQotUAyN3Gb4ar7WXZ4DEb2wPZsSybkK1sf",
		"/ip4/104.131.131.82/tcp/4001/ipfs/QmaCpDMGvV2BGHeYERUEnRQAwe3N8SzbUtfsmvsqQLuvuJ",
		"/ip4/104.236.179.241/tcp/4001/ipfs/QmSoLPppuBtQSGwKDZT2M73ULpjvfd3aZ6ha4oFGL1KrGM",
		"/ip4/128.199.219.111/tcp/4001/ipfs/QmSoLSafTMBsPKadTEgaXctDQVcqN88CNLHXMkTNwMKPnu",
		"/ip4/178.62.158.247/tcp/4001/ipfs/QmSoLer265NRgSp2LA3dPaeykiS1J6DifTC88f5uVQKNAd",
	})
	ipfsPeer0 := ipfsPeers[0]
	ipfsPeer1 := ipfsPeers[1]

	nodes := makeNodes(t, ctx, 2)

	nodes[0].Peerstore().AddAddrs(ipfsPeer0.ID, ipfsPeer0.Addrs, pstore.PermanentAddrTTL)
	nodes[0].Connect(context.Background(), ipfsPeer0)
	if len(nodes[0].Network().ConnsToPeer(ipfsPeer0.ID)) == 0 {
		t.Error()
	}
	nodes[1].Peerstore().AddAddrs(ipfsPeer1.ID, ipfsPeer1.Addrs, pstore.PermanentAddrTTL)
	nodes[1].Connect(context.Background(), ipfsPeer1)
	if len(nodes[1].Network().ConnsToPeer(ipfsPeer1.ID)) == 0 {
		t.Error()
	}
	node0PeerInfo := pstore.PeerInfo{
		ID:    nodes[0].ID(),
		Addrs: []ma.Multiaddr{},
	}
	// ensure connection: node0 <-> ipfsPeer0 <-> ipfsPeer1 <-> node1
	if len(nodes[0].Network().ConnsToPeer(ipfsPeer1.ID)) != 0 {
		t.Error()
	}
	if len(nodes[1].Network().ConnsToPeer(ipfsPeer0.ID)) != 0 {
		t.Error()
	}
	if len(nodes[1].Network().ConnsToPeer(nodes[0].ID())) != 0 {
		t.Error()
	}

	nodes[1].Connect(context.Background(), node0PeerInfo)

	if len(nodes[1].Network().ConnsToPeer(nodes[0].ID())) == 0 {
		t.Error()
	}
}

func TestListenShardConnectingPeers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	nodes := makeNodes(t, ctx, 3)
	connectBarbell(t, ctx, nodes)
	waitForPubSubMeshBuilt()
	// 0 <-> 1 <-> 2
	nodes[0].ListenShard(ctx, 0)
	nodes[0].PublishListeningShards(ctx)
	time.Sleep(time.Millisecond * 100)
	nodes[2].ListenShard(ctx, 42)
	nodes[2].PublishListeningShards(ctx)
	time.Sleep(time.Millisecond * 100)
	connWithNode2 := nodes[0].Network().ConnsToPeer(nodes[2].ID())
	if len(connWithNode2) != 0 {
		t.Error("Node 0 shouldn't have connection with node 2")
	}
	nodes[0].ListenShard(ctx, 42)
	nodes[0].PublishListeningShards(ctx)
	time.Sleep(time.Second * 1)
	connWithNode2 = nodes[0].Network().ConnsToPeer(nodes[2].ID())
	if len(connWithNode2) == 0 {
		t.Error("Node 0 should have connected to node 2 after listening to shard 42")
	}
}

func TestDHTBootstrapping(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bootnode := makeUnbootstrappedNode(t, ctx, 0)
	piBootnode := pstore.PeerInfo{
		ID:    bootnode.ID(),
		Addrs: bootnode.Addrs(),
	}
	node1 := makeUnbootstrappedNode(t, ctx, 1)
	connect(t, ctx, bootnode, node1)
	node2 := makeTestingNode(t, ctx, 2, true, []pstore.PeerInfo{piBootnode})
	time.Sleep(time.Millisecond * 100)
	if len(node2.Peerstore().PeerInfo(node1.ID()).Addrs) == 0 {
		t.Error("node2 should have known node1 through the bootnode")
	}
}

type eventTestServer struct {
	pbevent.EventServer
	receivedData chan []byte
}

func makeEventResponse(success bool) *pbevent.Response {
	var status pbevent.Response_Status
	var msg string
	if success {
		status = pbevent.Response_SUCCESS
		msg = ""
	} else {
		status = pbevent.Response_FAILURE
		msg = "failed"
	}
	return &pbevent.Response{Status: status, Message: msg}
}

func (s *eventTestServer) Receive(
	ctx context.Context,
	req *pbevent.ReceiveRequest) (*pbevent.ReceiveResponse, error) {
	go func() {
		s.receivedData <- req.Data
	}()
	var resBytes []byte
	success := true
	switch int(req.MsgType) {
	case typeCollation:
		msg := &pbmsg.Collation{}
		err := proto.Unmarshal(req.Data, msg)
		if err != nil {
			success = false
		}
		resBytes = []byte{1} // true
		fmt.Println("received collation: ", msg)
	case typeCollationRequest:
		msg := &pbmsg.CollationRequest{}
		err := proto.Unmarshal(req.Data, msg)
		if err != nil {
			success = false
		}
		if msg.ShardID != 42 {
			collation := &pbmsg.Collation{
				ShardID: msg.ShardID,
				Period:  msg.Period,
				Blobs:   []byte(fmt.Sprintf("%v:%v:blobssss", msg.ShardID, msg.Period)),
			}
			resBytes, err = proto.Marshal(collation)
			if err != nil {
				success = false
			}
		} else {
			success = false
			// resBytes = []byte{} // return empty bytes to indicate that it doesn't has the collation
		}
	default:
		success = false
	}
	return &pbevent.ReceiveResponse{
		Response: makeEventResponse(success),
		Data:     resBytes,
	}, nil
}

func runEventServer(ctx context.Context, eventRPCAddr string) (*eventTestServer, error) {
	lis, err := net.Listen("tcp", eventRPCAddr)
	if err != nil {
		return nil, err
	}
	s := grpc.NewServer()
	ets := &eventTestServer{receivedData: make(chan []byte)}
	pbevent.RegisterEventServer(
		s,
		ets,
	)
	go s.Serve(lis)
	go func() {
		select {
		case <-ctx.Done():
			s.Stop()
		}
	}()
	return ets, nil
}

func TestEventRPCPubSub(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	nodes := makeNodes(t, ctx, 2)
	connectBarbell(t, ctx, nodes)
	shardID := ShardIDType(1)
	nodes[0].ListenShard(ctx, shardID)
	nodes[1].ListenShard(ctx, shardID)
	waitForPubSubMeshBuilt()
	eventRPCPort := 35566
	notifierAddr := fmt.Sprintf("127.0.0.1:%v", eventRPCPort)
	eventNotifier, err := NewRpcEventNotifier(ctx, notifierAddr)
	if err != nil {
		t.Error("failed to create event notifier")
	}
	// explicitly set the eventNotifier
	nodes[1].eventNotifier = eventNotifier
	nodes[0].broadcastCollation(ctx, shardID, 1, []byte{})
	time.Sleep(time.Second * 1)
}

func TestEventRPCNotifier(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventRPCPort := 55666
	notifierAddr := fmt.Sprintf("127.0.0.1:%v", eventRPCPort)
	_, err := runEventServer(ctx, notifierAddr)
	if err != nil {
		t.Error("failed to run event server")
	}
	time.Sleep(time.Millisecond * 100)
	eventNotifier, err := NewRpcEventNotifier(ctx, notifierAddr)
	if err != nil {
		t.Error(err)
	}

	shardID := ShardIDType(1)
	period := 2

	// send collation to the Host side
	collation := &pbmsg.Collation{
		ShardID: shardID,
		Period:  PBInt(period),
		Blobs:   []byte("123"),
	}
	collationBytes := protoToBytes(t, collation)
	isValidBytes, err := eventNotifier.Receive("", typeCollation, collationBytes)
	if err != nil {
		if err != nil {
			t.Errorf("something wrong when calling `eventNotifier.Receive`: %v", err)
		}
	}
	if len(isValidBytes) != 1 && isValidBytes[0] != 0 {
		t.Errorf("wrong reponse from `eventNotifier`")
	}

	// GetCollation
	req := &pbmsg.CollationRequest{
		ShardID: shardID,
		Period:  PBInt(period),
		Hash:    "123",
	}
	reqBytes := protoToBytes(t, req)
	respBytes, err := eventNotifier.Receive("", typeCollationRequest, reqBytes)
	if err != nil {
		t.Errorf("something wrong when calling `eventNotifier.Receive`: %v", err)
	}
	collationReceived := &pbmsg.Collation{}
	err = proto.Unmarshal(respBytes, collationReceived)
	if err != nil {
		t.Errorf("failed to unmarshal collation bytes")
	}
	if ShardIDType(collationReceived.ShardID) != shardID &&
		int(collationReceived.Period) != period {
		t.Errorf("wrong response from `eventNotifier.GetCollation`")
	}
}

func TestSubscribeCollationWithRPCEventNotifier(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	nodes := makeNodes(t, ctx, 3)
	connectBarbell(t, ctx, nodes)
	shardID := ShardIDType(1)
	nodes[0].ListenShard(ctx, shardID)
	nodes[1].ListenShard(ctx, shardID)
	nodes[2].ListenShard(ctx, shardID)
	waitForPubSubMeshBuilt()

	// setup 2 event server and notifier(stub), for node1 and node2 respectively
	eventRPCPort := 55667
	notifierAddr := fmt.Sprintf("127.0.0.1:%v", eventRPCPort)
	s1, err := runEventServer(ctx, notifierAddr)
	if err != nil {
		t.Error("failed to run event server")
	}
	eventNotifier, err := NewRpcEventNotifier(ctx, notifierAddr)
	if err != nil {
		t.Error("failed to create event notifier")
	}
	// explicitly set the eventNotifier
	nodes[1].eventNotifier = eventNotifier

	eventRPCPort++
	notifierAddr = fmt.Sprintf("127.0.0.1:%v", eventRPCPort)
	s2, err := runEventServer(ctx, notifierAddr)
	if err != nil {
		t.Error("failed to run event server")
	}
	eventNotifier, err = NewRpcEventNotifier(ctx, notifierAddr)
	if err != nil {
		t.Error("failed to create event notifier")
	}
	// explicitly set the eventNotifier
	nodes[2].eventNotifier = eventNotifier

	// TODO: should be sure when is the validator executed, and how it will affect relaying
	err = nodes[0].broadcastCollation(ctx, shardID, 1, []byte{})
	if err != nil {
		t.Errorf("failed to broadcast collation in nodes[0]: %v", err)
	}
	select {
	case dataBytes := <-s1.receivedData:
		collation := &pbmsg.Collation{}
		err = proto.Unmarshal(dataBytes, collation)
		if err != nil {
			t.Errorf("failed to unmarshal collation bytes")
		}
		if ShardIDType(collation.ShardID) != shardID || int(collation.Period) != 1 {
			t.Error("node1 received wrong collations")
		}
	case <-time.After(time.Second * 5):
		t.Error("timeout in node1")
	}
	select {
	case dataBytes := <-s2.receivedData:
		collation := &pbmsg.Collation{}
		err = proto.Unmarshal(dataBytes, collation)
		if err != nil {
			t.Errorf("failed to unmarshal collation bytes")
		}
		if ShardIDType(collation.ShardID) != shardID || int(collation.Period) != 1 {
			t.Error("node2 received wrong collations")
		}
	case <-time.After(time.Second * 5):
		t.Error("timeout in node2")
	}
}

func TestRequestCollationWithRPCEventNotifier(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	nodes := makeNodes(t, ctx, 2)
	connectBarbell(t, ctx, nodes)

	// case: without RPCEventNotifier set
	shardID := ShardIDType(1)
	period := 2
	req := &pbmsg.CollationRequest{
		ShardID: shardID,
		Period:  PBInt(period),
		Hash:    "",
	}
	reqBytes := protoToBytes(t, req)
	_, err := nodes[0].generalRequest(ctx, nodes[1].ID(), typeCollationRequest, reqBytes)
	// `err != nil` because nodes[1] hasn't set the eventNotifier, failing to handle the request
	if err == nil {
		t.Errorf(
			"should be unable to unmarshal because nodes[1] should send invalid collation, due to the lack of an eventNotifier",
		)
	}

	// case: with RPCEventNotifier set, db in the event server has the collation
	notifierAddr := fmt.Sprintf("127.0.0.1:%v", 55669)
	_, err = runEventServer(ctx, notifierAddr)
	if err != nil {
		t.Error("failed to run event server")
	}
	eventNotifier, err := NewRpcEventNotifier(ctx, notifierAddr)
	if err != nil {
		t.Error("failed to create event notifier")
	}
	// explicitly set the eventNotifier
	nodes[1].eventNotifier = eventNotifier
	req = &pbmsg.CollationRequest{
		ShardID: shardID,
		Period:  PBInt(period),
		Hash:    "",
	}
	reqBytes = protoToBytes(t, req)
	respBytes, err := nodes[0].generalRequest(ctx, nodes[1].ID(), typeCollationRequest, reqBytes)
	if err != nil {
		t.Errorf("request collation failed: %v", err)
	}
	collation := &pbmsg.Collation{}
	err = proto.Unmarshal(respBytes, collation)
	if err != nil {
		t.Errorf("failed to unmarshal collation bytes")
	}

	if collation.ShardID != shardID || int(collation.Period) != period {
		t.Errorf(
			"responded collation does not correspond to the request: collation.ShardID=%v, request.shardID=%v, collation.Period=%v, request.period=%v",
			collation.ShardID,
			shardID,
			collation.Period,
			period,
		)
	}

	// case: with RPCEventNotifier set, db in the event server doesn't have the collation
	periodNotFound := ShardIDType(42)
	req = &pbmsg.CollationRequest{
		ShardID: periodNotFound,
		Period:  PBInt(period),
		Hash:    "",
	}
	reqBytes = protoToBytes(t, req)
	respBytes, err = nodes[0].generalRequest(ctx, nodes[1].ID(), typeCollationRequest, reqBytes)
	if err == nil {
		t.Errorf(
			"should be unable to unmarshal because nodes[1] should send invalid collation, due to the lack of an eventNotifier",
		)
	}
}
