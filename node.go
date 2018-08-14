package main

import (
	"context"
	"fmt"

	host "github.com/libp2p/go-libp2p-host"
	peer "github.com/libp2p/go-libp2p-peer"
	ma "github.com/multiformats/go-multiaddr"
)

// node client version
const clientVersion = "sharding-p2p-node/0.0.1"

// Node type - a p2p host implementing one or more p2p protocols
type Node struct {
	host.Host        // lib-p2p host
	*AddPeerProtocol // addpeer protocol impl
	*RequestProtocol // for peers to request data
	*ShardManager

	number int
}

// NewNode creates a new node with its implemented protocols
func NewNode(ctx context.Context, host host.Host, number int) *Node {
	node := &Node{Host: host, number: number}
	node.AddPeerProtocol = NewAddPeerProtocol(node)
	node.RequestProtocol = NewRequestProtocol(node)
	node.ShardManager = NewShardManager(ctx, node)
	return node
}

func (n *Node) Name() string {
	id := n.ID().Pretty()
	return fmt.Sprintf("<Node %d %s>", n.number, id[2:8])
}

func (n *Node) GetFullAddr() string {
	hostAddr, _ := ma.NewMultiaddr(fmt.Sprintf("/ipfs/%s", n.ID().Pretty()))

	// Now we can build a full multiaddress to reach this host
	// by encapsulating both addresses:
	addr := n.Addrs()[0]
	fullAddr := addr.Encapsulate(hostAddr)
	return fullAddr.String()
}

// TODO: should be changed to `Knows` and `HasConnections`
func (n *Node) IsPeer(peerID peer.ID) bool {
	for _, value := range n.Network().Peers() {
		if value == peerID {
			return true
		}
	}
	return false
}
