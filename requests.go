package main

import (
	"bufio"
	"context"
	"log"

	pbmsg "github.com/ethresearch/sharding-p2p-poc/pb"
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

// NewRequestProtocol defines the request protocol, which allows others to query data
func NewRequestProtocol(node *Node) *RequestProtocol {
	p := &RequestProtocol{
		node: node,
	}
	node.SetStreamHandler(collationRequestProtocol, p.onCollationRequest)
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

// remote peer requests handler
func (p *RequestProtocol) onCollationRequest(s inet.Stream) {
	// defer inet.FullClose(s)
	// reject if the sender is not a peer
	data := &pbmsg.CollationRequest{}
	decoder := protobufCodec.Multicodec(nil).Decoder(bufio.NewReader(s))
	err := decoder.Decode(data)
	if err != nil {
		log.Println("onCollationRequest: ", err)
		return
	}

	// FIXME: add checks
	var collation *pbmsg.Collation
	collation, err = p.getCollation(
		data.GetShardID(),
		data.GetPeriod(),
		data.GetHash(),
	)
	var collationResp *pbmsg.CollationResponse
	if err != nil {
		collationResp = &pbmsg.CollationResponse{
			Success:   false,
			Collation: nil,
		}
	} else {
		collationResp = &pbmsg.CollationResponse{
			Success:   true,
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
