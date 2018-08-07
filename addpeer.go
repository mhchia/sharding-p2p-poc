package main

import (
	"bufio"
	"context"
	"fmt"
	"log"

	inet "github.com/libp2p/go-libp2p-net"
	pstore "github.com/libp2p/go-libp2p-peerstore"

	pbmsg "github.com/ethresearch/sharding-p2p-poc/pb/message"

	protobufCodec "github.com/multiformats/go-multicodec/protobuf"
)

// pattern: /protocol-name/request-or-response-message/version
const addPeerRequest = "/addPeer/request/0.0.1"
const addPeerResponse = "/addPeer/response/0.0.1"

// AddPeerProtocol type
type AddPeerProtocol struct {
	node *Node     // local host
	done chan bool // only for demo purposes to stop main from terminating
}

func NewAddPeerProtocol(node *Node) *AddPeerProtocol {
	p := &AddPeerProtocol{
		node: node,
		done: make(chan bool),
	}
	node.SetStreamHandler(addPeerRequest, p.onRequest)
	node.SetStreamHandler(addPeerResponse, p.onResponse)
	return p
}

// remote peer requests handler
func (p *AddPeerProtocol) onRequest(s inet.Stream) {
	// get request data
	data := &pbmsg.AddPeerRequest{}
	decoder := protobufCodec.Multicodec(nil).Decoder(bufio.NewReader(s))
	err := decoder.Decode(data)
	if err != nil {
		log.Println(err)
		return
	}

	log.Printf(
		"%s: Received addPeer request from %s. Message: %s",
		p.node.Name(),
		s.Conn().RemotePeer(),
		data.Message,
	)

	// generate response message
	log.Printf(
		"%s: Sending addPeer response to %s",
		p.node.Name(),
		s.Conn().RemotePeer(),
	)

	resp := &pbmsg.AddPeerResponse{
		Response: &pbmsg.Response{Status: pbmsg.Response_SUCCESS},
	}

	p.node.Peerstore().AddAddr(
		s.Conn().RemotePeer(),
		s.Conn().RemoteMultiaddr(),
		pstore.PermanentAddrTTL,
	)

	if ok := p.node.sendMessage(s.Conn().RemotePeer(), addPeerResponse, resp); !ok {

	}
}

// remote addPeer response handler
func (p *AddPeerProtocol) onResponse(s inet.Stream) {
	data := &pbmsg.AddPeerResponse{}
	decoder := protobufCodec.Multicodec(nil).Decoder(bufio.NewReader(s))
	err := decoder.Decode(data)
	if err != nil {
		return
	}

	log.Printf(
		"%s: Received addPeer response from %s, result=%v",
		p.node.Name(),
		s.Conn().RemotePeer(),
		data.Response.Status,
	)
	p.done <- true
}

func (p *AddPeerProtocol) AddPeer(peerAddr string) bool {
	peerid, targetAddr := parseAddr(peerAddr)
	log.Printf("%s: Sending addPeer to: %s....", p.node.Name(), peerid)
	p.node.Peerstore().AddAddr(peerid, targetAddr, pstore.PermanentAddrTTL)
	// create message data
	req := &pbmsg.AddPeerRequest{
		Message: fmt.Sprintf("AddPeer from %s", p.node.Name()),
	}

	s, err := p.node.NewStream(context.Background(), peerid, addPeerRequest)
	if err != nil {
		log.Println(err)
		return false
	}

	if ok := sendProtoMessage(req, s); !ok {
		return false
	}

	// store ref request so response handler has access to it
	// p.requests[req.MessageData.Id] = req
	// log.Printf("%s: AddPeer to: %s was sent. Message Id: %s, Message: %s", p.node.Name(), host.Name(), req.MessageData.Id, req.Message)
	return true

}
