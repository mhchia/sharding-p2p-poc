package main

import (
	"bufio"
	"context"
	"fmt"
	"log"

	pbmsg "github.com/ethresearch/sharding-p2p-poc/pb/message"
	"github.com/golang/protobuf/proto"

	inet "github.com/libp2p/go-libp2p-net"
	peer "github.com/libp2p/go-libp2p-peer"

	protocol "github.com/libp2p/go-libp2p-protocol"
	protobufCodec "github.com/multiformats/go-multicodec/protobuf"
)

// RequestProtocol type
type RequestProtocol struct {
	node *Node
}

const collationRequestProtocol = protocol.ID("/collationRequest/1.0.0")
const shardPeerRequestProtocol = protocol.ID("/shardPeerRequest/1.0.0")

// NewRequestProtocol defines the request protocol, which allows others to query data
func NewRequestProtocol(node *Node) *RequestProtocol {
	p := &RequestProtocol{
		node: node,
	}
	node.SetStreamHandler(collationRequestProtocol, p.onCollationRequest)
	node.SetStreamHandler(shardPeerRequestProtocol, p.onShardPeerRequest)
	return p
}

func (p *RequestProtocol) getCollation(
	shardID ShardIDType,
	period int64,
	collationHash string) (*pbmsg.Collation, error) {
	// FIXME: fake response for now. Shuld query from the saved data.
	return &pbmsg.Collation{
		ShardID: shardID,
		Period:  period,
		Blobs:   "",
	}, nil
}

func readProtoMessage(data proto.Message, s inet.Stream) bool {
	decoder := protobufCodec.Multicodec(nil).Decoder(bufio.NewReader(s))
	err := decoder.Decode(data)
	if err != nil {
		log.Println("readProtoMessage: ", err)
		return false
	}
	log.Println("!@# readProtoMessage: ", data)
	return true
}

func (p *RequestProtocol) onShardPeerRequest(s inet.Stream) {
	req := &pbmsg.ShardPeerRequest{}
	if ok := readProtoMessage(req, s); !ok {
		s.Close()
		return
	}
	peerIDs := p.node.GetNodesInShard(req.ShardID)
	peerIDStrings := []string{}
	for _, peerID := range peerIDs {
		peerIDStrings = append(peerIDStrings, peerID.Pretty())
	}
	res := &pbmsg.ShardPeerResponse{
		Response: &pbmsg.Response{Status: pbmsg.Response_SUCCESS},
		Peers:    peerIDStrings,
	}
	resBytes, err := proto.Marshal(res)
	if err != nil {
		s.Close()
		return
	}
	s.Write(resBytes)
}

func (p *RequestProtocol) requestShardPeer(
	ctx context.Context,
	peerID peer.ID,
	shardID ShardIDType) ([]peer.ID, error) {
	s, err := p.node.NewStream(
		ctx,
		peerID,
		shardPeerRequestProtocol,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to open new stream")
	}
	req := &pbmsg.ShardPeerRequest{
		ShardID: shardID,
	}
	if ok := sendProtoMessage(req, s); !ok {
		return nil, fmt.Errorf("failed to send request")
	}
	res := &pbmsg.ShardPeerResponse{}
	bytes := []byte{}
	_, err = s.Read(bytes)
	if ok := readProtoMessage(res, s); !ok {
		s.Close()
		return nil, fmt.Errorf("failed to read response proto")
	}
	peerIDs := []peer.ID{}
	for _, peerString := range res.Peers {
		peerID, err := peer.IDB58Decode(peerString)
		if err != nil {
			return nil, fmt.Errorf("error occurred when parsing peerIDs")
		}
		peerIDs = append(peerIDs, peerID)
	}
	return peerIDs, nil
}

// collation request
func (p *RequestProtocol) onCollationRequest(s inet.Stream) {
	// defer inet.FullClose(s)
	// reject if the sender is not a peer
	data := &pbmsg.CollationRequest{}
	if ok := readProtoMessage(data, s); !ok {
		s.Close()
		return
	}

	// FIXME: add checks
	var collation *pbmsg.Collation
	collation, err := p.getCollation(
		data.GetShardID(),
		data.GetPeriod(),
		data.GetHash(),
	)
	var collationResp *pbmsg.CollationResponse
	if err != nil {
		collationResp = &pbmsg.CollationResponse{
			Response:  &pbmsg.Response{Status: pbmsg.Response_FAILURE},
			Collation: nil,
		}
	} else {
		collationResp = &pbmsg.CollationResponse{
			Response:  &pbmsg.Response{Status: pbmsg.Response_SUCCESS},
			Collation: collation,
		}
	}
	collationRespBytes, err := proto.Marshal(collationResp)
	if err != nil {
		s.Close()
	}
	s.Write(collationRespBytes)
	log.Printf(
		"%v: Sent %v to %v",
		p.node.Name(),
		collationResp,
		s.Conn().RemotePeer(),
	)
}

func (p *RequestProtocol) sendCollationRequest(
	peerID peer.ID,
	shardID ShardIDType,
	period int64,
	blobs string) bool {
	// create message data
	req := &pbmsg.CollationRequest{
		ShardID: shardID,
		Period:  period,
	}
	return p.sendCollationMessage(peerID, req)
}

func (p *RequestProtocol) sendCollationMessage(peerID peer.ID, req *pbmsg.CollationRequest) bool {
	log.Printf("%s: Sending collationReq to: %s....", p.node.ID(), peerID)
	s, err := p.node.NewStream(
		context.Background(),
		peerID,
		collationRequestProtocol,
	)
	if err != nil {
		log.Println(err)
		return false
	}

	if ok := sendProtoMessage(req, s); !ok {
		return false
	}

	return true
}

// shard preference
