package main

import (
	"context"
	"fmt"
	"log"
	"sync"

	floodsub "github.com/libp2p/go-floodsub"
	peer "github.com/libp2p/go-libp2p-peer"
	pstore "github.com/libp2p/go-libp2p-peerstore"

	pbmsg "github.com/ethresearch/sharding-p2p-poc/pb/message"
	"github.com/golang/protobuf/proto"
)

type ShardManager struct {
	node *Node // local host

	pubsubService       *floodsub.PubSub
	listeningShardsSub  *floodsub.Subscription
	shardCollationsSubs map[ShardIDType]*floodsub.Subscription
	collations          map[string]struct{}
	lock                sync.Mutex

	peerListeningShards map[peer.ID]*ListeningShards // TODO: handle the case when peer leave
}

const listeningShardTopic = "listeningShard"
const collationTopicFmt = "shardCollations_%d"

func getCollationsTopic(shardID ShardIDType) string {
	return fmt.Sprintf(collationTopicFmt, shardID)
}

func NewShardManager(ctx context.Context, node *Node) *ShardManager {
	service, err := floodsub.NewGossipSub(ctx, node.Host)
	if err != nil {
		log.Fatalln(err)
	}
	p := &ShardManager{
		node:                node,
		pubsubService:       service,
		listeningShardsSub:  nil,
		shardCollationsSubs: make(map[ShardIDType]*floodsub.Subscription),
		collations:          make(map[string]struct{}),
		lock:                sync.Mutex{},
		peerListeningShards: make(map[peer.ID]*ListeningShards),
	}
	p.SubscribeListeningShards()
	p.ListenListeningShards(ctx)
	return p
}

// management for peers' listening shards

func (n *ShardManager) AddPeerListeningShard(peerID peer.ID, shardID ShardIDType) {
	if shardID >= numShards {
		return
	}
	if n.IsPeerListeningShard(peerID, shardID) {
		return
	}
	if _, prs := n.peerListeningShards[peerID]; !prs {
		n.peerListeningShards[peerID] = NewListeningShards()
	}
	n.peerListeningShards[peerID].setShard(shardID)
}

func (n *ShardManager) RemovePeerListeningShard(peerID peer.ID, shardID ShardIDType) {
	if !n.IsPeerListeningShard(peerID, shardID) {
		return
	}
	n.peerListeningShards[peerID].unsetShard(shardID)
}

func (n *ShardManager) GetPeerListeningShard(peerID peer.ID) []ShardIDType {
	if _, prs := n.peerListeningShards[peerID]; !prs {
		return make([]ShardIDType, 0)
	}
	return n.peerListeningShards[peerID].getShards()
}

func (n *ShardManager) SetPeerListeningShard(peerID peer.ID, shardIDs []ShardIDType) {
	listeningShards := n.GetPeerListeningShard(peerID)
	for _, shardID := range listeningShards {
		n.RemovePeerListeningShard(peerID, shardID)
	}
	for _, shardID := range shardIDs {
		n.AddPeerListeningShard(peerID, shardID)
	}
}

func (n *ShardManager) IsPeerListeningShard(peerID peer.ID, shardID ShardIDType) bool {
	if _, prs := n.peerListeningShards[peerID]; !prs {
		return false
	}
	shards := n.GetPeerListeningShard(peerID)
	return inShards(shardID, shards)
}

func (n *ShardManager) GetNodesInShard(shardID ShardIDType) []peer.ID {
	peers := []peer.ID{}
	for peerID, listeningShards := range n.peerListeningShards {
		if listeningShards.isShardSet(shardID) {
			peers = append(peers, peerID)
		}
	}
	return peers
}

// listen/unlisten shards

func (n *ShardManager) connectShardNodes(shardID ShardIDType) error {
	peerIDs := n.GetNodesInShard(shardID)
	pinfos := []pstore.PeerInfo{}
	for _, peerID := range peerIDs {
		// don't connect ourselves
		if peerID == n.node.ID() {
			continue
		}
		// if already have conns, no need to add them
		connsToPeer := n.node.Network().ConnsToPeer(peerID)
		if len(connsToPeer) != 0 {
			continue
		}
		pi := n.node.Peerstore().PeerInfo(peerID)
		pinfos = append(pinfos, pi)
	}

	// borrowed from `bootstrapConnect`, should be modified/refactored and tested
	errs := make(chan error, len(pinfos))
	var wg sync.WaitGroup
	ctx := context.Background()
	for _, p := range pinfos {
		wg.Add(1)
		go func(p pstore.PeerInfo) {
			defer wg.Done()
			if err := n.node.Connect(ctx, p); err != nil {
				log.Printf(
					"Failed to connect peer %v in shard %v: %s",
					p.ID,
					shardID,
					err,
				)
				errs <- err
				return
			}
			log.Printf("Successfully connected peer %v in shard %v", p.ID, shardID)
		}(p)
	}
	wg.Wait()
	// FIXME: ignore the errors when connecting shard peers for now
	return nil
}

func (n *ShardManager) ListenShard(ctx context.Context, shardID ShardIDType) {
	if n.IsShardListened(shardID) {
		return
	}
	n.AddPeerListeningShard(n.node.ID(), shardID)

	// TODO: should set a critiria: if we have enough peers in the shard, don't connect shard nodes
	n.connectShardNodes(shardID)
	n.PublishListeningShards()

	// shardCollations protocol
	n.SubscribeShardCollations(shardID)
	n.ListenShardCollations(ctx, shardID)
}

func (n *ShardManager) UnlistenShard(shardID ShardIDType) {
	if !n.IsShardListened(shardID) {
		return
	}
	n.RemovePeerListeningShard(n.node.ID(), shardID)

	// listeningShards protocol
	// TODO: should we remove some peers in this shard?
	n.PublishListeningShards()

	// shardCollations protocol
	n.UnsubscribeShardCollations(shardID)
}

func (n *ShardManager) GetListeningShards() []ShardIDType {
	return n.GetPeerListeningShard(n.node.ID())
}

func (n *ShardManager) IsShardListened(shardID ShardIDType) bool {
	return inShards(shardID, n.GetListeningShards())
}

func inShards(shardID ShardIDType, shards []ShardIDType) bool {
	for _, value := range shards {
		if value == shardID {
			return true
		}
	}
	return false
}

//
// PubSub related
//

// listeningShards notification

func (n *ShardManager) ListenListeningShards(ctx context.Context) {
	// this is necessary, because n.listeningShardsSub might be set to `nil`
	// after `UnsubscribeListeningShards`
	listeningShardsSub := n.listeningShardsSub
	go func() {
		for {
			msg, err := listeningShardsSub.Next(ctx)
			if err != nil {
				// Will enter here if
				// 	1. `sub.Cancel()` is called with
				//		err="subscription cancelled by calling sub.Cancel()"
				// 	2. ctx is cancelled with err="context canceled"
				// log.Print("ListenListeningShards: ", err)
				return
			}
			// TODO: check if `peerID` is the node itself
			peerID := msg.GetFrom()
			if peerID == n.node.ID() {
				continue
			}
			listeningShards := ListeningShardsFromBytes(msg.GetData())
			n.SetPeerListeningShard(peerID, listeningShards.getShards())
			// log.Printf(
			// 	"%v: receive: peerID=%v, listeningShards=%v",
			// 	n.node.Name(),
			// 	peerID,
			// 	listeningShards.getShards(),
			// )
		}
	}()
}

func (n *ShardManager) SubscribeListeningShards() {
	listeningShardsSub, err := n.pubsubService.Subscribe(listeningShardTopic)
	if err != nil {
		log.Fatal(err)
	}
	n.listeningShardsSub = listeningShardsSub
}

func (n *ShardManager) UnsubscribeListeningShards() {
	n.listeningShardsSub.Cancel()
	n.listeningShardsSub = nil
}

func (n *ShardManager) PublishListeningShards() {
	selfListeningShards, prs := n.peerListeningShards[n.node.ID()]
	if !prs {
		selfListeningShards = NewListeningShards()
	}
	bytes := selfListeningShards.ToBytes()
	n.pubsubService.Publish(listeningShardTopic, bytes)
}

// shard collations

func (n *ShardManager) ListenShardCollations(ctx context.Context, shardID ShardIDType) {
	if !n.IsShardCollationsSubscribed(shardID) {
		return
	}
	shardCollationsSub := n.shardCollationsSubs[shardID]
	numCollationReceived := 0
	go func() {
		for {
			// TODO: consider to pass the context from outside?
			msg, err := shardCollationsSub.Next(ctx)
			if err != nil {
				// log.Print(err)
				return
			}
			// TODO: check if `peerID` is the node itself
			peerID := msg.GetFrom()
			if peerID == n.node.ID() {
				continue
			}
			bytes := msg.GetData()
			collation := pbmsg.Collation{}
			err = proto.Unmarshal(bytes, &collation)
			if err != nil {
				// log.Fatal(err)
				continue
			}
			// TODO: need some check against collations
			collationHash := GetCollationHash(&collation)
			numCollationReceived++
			// n.lock.Lock()
			// n.collations[collationHash] = struct{}{}
			// // log.Printf(
			// // 	"%v: current numCollations=%d",
			// // 	n.node.Name(),
			// // 	len(n.collations),
			// // )
			// n.lock.Unlock()
			log.Printf(
				"%v: receive: collation: seqNo=%v, hash=%v, shardId=%v, number=%v",
				n.node.Name(),
				numCollationReceived,
				collationHash[:8],
				collation.GetShardID(),
				collation.GetPeriod(),
			)
		}
	}()
}

func (n *ShardManager) IsShardCollationsSubscribed(shardID ShardIDType) bool {
	_, prs := n.shardCollationsSubs[shardID]
	return prs && (n.shardCollationsSubs[shardID] != nil)
}

func (n *ShardManager) SubscribeShardCollations(shardID ShardIDType) {
	if n.IsShardCollationsSubscribed(shardID) {
		return
	}
	collationsTopic := getCollationsTopic(shardID)
	collationsSub, err := n.pubsubService.Subscribe(collationsTopic)
	if err != nil {
		log.Fatal(err)
	}
	n.shardCollationsSubs[shardID] = collationsSub
}

func (n *ShardManager) broadcastCollation(shardID ShardIDType, period int64, blobs []byte) bool {
	// create message data
	data := &pbmsg.Collation{
		ShardID: shardID,
		Period:  period,
		Blobs:   blobs,
	}
	return n.broadcastCollationMessage(data)
}

func (n *ShardManager) broadcastCollationMessage(collation *pbmsg.Collation) bool {
	if !n.IsShardCollationsSubscribed(collation.GetShardID()) {
		return false
	}
	collationsTopic := getCollationsTopic(collation.ShardID)
	bytes, err := proto.Marshal(collation)
	if err != nil {
		log.Fatal(err)
		return false
	}
	err = n.pubsubService.Publish(collationsTopic, bytes)
	if err != nil {
		log.Fatal(err)
		return false
	}
	return true
}

func (n *ShardManager) UnsubscribeShardCollations(shardID ShardIDType) {
	// TODO: unsubscribe in pubsub
	if n.IsShardCollationsSubscribed(shardID) {
		n.shardCollationsSubs[shardID].Cancel()
		n.shardCollationsSubs[shardID] = nil
	}
}
