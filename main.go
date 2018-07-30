package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	mrand "math/rand"
	"strconv"
	"strings"

	// gologging "gx/ipfs/QmQvJiADDe7JR4m968MwXobTCCzUqQkP87aRHe29MEBGHV/go-logging"
	ds "github.com/ipfs/go-datastore"
	dsync "github.com/ipfs/go-datastore/sync"
	libp2p "github.com/libp2p/go-libp2p"
	crypto "github.com/libp2p/go-libp2p-crypto"
	ic "github.com/libp2p/go-libp2p-crypto"
	peer "github.com/libp2p/go-libp2p-peer"
	pstore "github.com/libp2p/go-libp2p-peerstore"
	rhost "github.com/libp2p/go-libp2p/p2p/host/routed"
	opentracing "github.com/opentracing/opentracing-go"

	"sourcegraph.com/sourcegraph/appdash"
	appdashtracer "sourcegraph.com/sourcegraph/appdash/opentracing"

	kaddht "github.com/libp2p/go-libp2p-kad-dht"
)

type ShardIDType = int64

const numShards ShardIDType = 100

func makeKey(seed int64) (ic.PrivKey, peer.ID, error) {
	// If the seed is zero, use real cryptographic randomness. Otherwise, use a
	// deterministic randomness source to make generated keys stay the same
	// across multiple runs
	r := mrand.New(mrand.NewSource(seed))
	// r := rand.Reader

	// Generate a key pair for this host. We will use it at least
	// to obtain a valid host ID.
	priv, _, err := crypto.GenerateKeyPairWithReader(crypto.RSA, 2048, r)
	if err != nil {
		return nil, "", err
	}

	// Get the peer id
	pid, err := peer.IDFromPrivateKey(priv)
	if err != nil {
		return nil, "", err
	}
	return priv, pid, nil
}

// makeNode creates a LibP2P host with a random peer ID listening on the
// given multiaddress. It will use secio if secio is true.
func makeNode(
	ctx context.Context,
	listenPort int,
	randseed int64,
	doBootstrapping bool,
	bootstrapPeers []pstore.PeerInfo) (*Node, error) {
	// FIXME: should be set to localhost if we don't want to expose it to outside
	listenAddrString := fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", listenPort)

	priv, _, err := makeKey(randseed)
	if err != nil {
		return nil, err
	}

	basicHost, err := libp2p.New(
		ctx,
		libp2p.Identity(priv),
		libp2p.ListenAddrStrings(listenAddrString),
	)
	if err != nil {
		panic(err)
	}

	// Construct a datastore (needed by the DHT). This is just a simple, in-memory thread-safe datastore.
	dstore := dsync.MutexWrap(ds.NewMapDatastore())

	// Make the DHT
	dht := kaddht.NewDHT(ctx, basicHost, dstore)

	// Make the routed host
	routedHost := rhost.Wrap(basicHost, dht)

	if doBootstrapping {
		// try to connect to the chosen nodes
		bootstrapConnect(ctx, routedHost, bootstrapPeers)

		err = dht.Bootstrap(ctx)
		if err != nil {
			return nil, err
		}
	}

	// Make a host that listens on the given multiaddress
	node := NewNode(ctx, routedHost, int(randseed))

	log.Printf("I am %s\n", node.GetFullAddr())

	return node, nil
}

const (
	defaultListenPort = 10000
	defaultRPCPort    = 13000
)

func main() {
	// LibP2P code uses golog to log messages. They log with different
	// string IDs (i.e. "swarm"). We can control the verbosity level for
	// all loggers with:
	// golog.SetAllLoggers(gologging.INFO) // Change to DEBUG for extra info

	// Parse options from the command line

	seed := flag.Int64("seed", 0, "set random seed for id generation")
	listenPort := flag.Int(
		"port",
		defaultListenPort,
		"port listened by the node for incoming connections",
	)
	rpcPort := flag.Int("rpcport", defaultRPCPort, "rpc port listened by the rpc server")
	doBootstrapping := flag.Bool("bootstrap", false, "whether to do bootstrapping or not")
	bootnodesStr := flag.String("bootnodes", "", "multiaddresses of the bootnodes")
	isClient := flag.Bool("client", false, "is RPC client or server")
	flag.Parse()

	rpcAddr := fmt.Sprintf("127.0.0.1:%v", *rpcPort)

	if *isClient {
		runClient(rpcAddr, flag.Args())
	} else {
		var bootnodes []pstore.PeerInfo
		if *bootnodesStr == "" {
			bootnodes = []pstore.PeerInfo{}
		} else {
			bootnodes = convertPeers(strings.Split(*bootnodesStr, ","))
		}
		runServer(*listenPort, *seed, *doBootstrapping, bootnodes, rpcAddr)
	}
}

func runServer(
	listenPort int,
	seed int64,
	doBootstrapping bool,
	bootnodes []pstore.PeerInfo,
	rpcAddr string) {
	ctx := context.Background()
	node, err := makeNode(ctx, listenPort, seed, doBootstrapping, bootnodes)
	if err != nil {
		log.Fatal(err)
	}

	// Set up Opentracing and Appdash tracer
	remote_collector := appdash.NewRemoteCollector("localhost:8701")
	tracer := appdashtracer.NewTracer(remote_collector)
	opentracing.InitGlobalTracer(tracer)
	// End of tracer setup

	runRPCServer(node, rpcAddr)
}

func runClient(rpcAddr string, cliArgs []string) {
	if len(cliArgs) <= 0 {
		log.Fatalf("Client: wrong args")
		return
	}
	rpcCmd := cliArgs[0]
	rpcArgs := cliArgs[1:]
	if rpcCmd == "addpeer" {
		if len(rpcArgs) != 3 {
			log.Fatalf("Client: usage: addpeer ip port seed")
		}
		targetIP := rpcArgs[0]
		targetPort, err := strconv.Atoi(rpcArgs[1])
		targetSeed, err := strconv.ParseInt(rpcArgs[2], 10, 64)
		if err != nil {
			panic(err)
		}
		callRPCAddPeer(rpcAddr, targetIP, targetPort, targetSeed)
	} else if rpcCmd == "subshard" {
		if len(rpcArgs) == 0 {
			log.Fatalf("Client: usage: subshard shard0 shard1 ...")
		}
		shardIDs := []ShardIDType{}
		for _, shardIDString := range rpcArgs {
			shardID, err := strconv.ParseInt(shardIDString, 10, 64)
			if err != nil {
				panic(err)
			}
			shardIDs = append(shardIDs, shardID)
		}
		callRPCSubscribeShard(rpcAddr, shardIDs)
	} else if rpcCmd == "unsubshard" {
		if len(rpcArgs) == 0 {
			log.Fatalf("Client: usage: unsubshard shard0 shard1 ...")
		}
		shardIDs := []ShardIDType{}
		for _, shardIDString := range rpcArgs {
			shardID, err := strconv.ParseInt(shardIDString, 10, 64)
			if err != nil {
				panic(err)
			}
			shardIDs = append(shardIDs, shardID)
		}
		callRPCUnsubscribeShard(rpcAddr, shardIDs)
	} else if rpcCmd == "getsubshard" {
		callRPCGetSubscribedShard(rpcAddr)
	} else if rpcCmd == "broadcastcollation" {
		if len(rpcArgs) != 4 {
			log.Fatalf(
				"Client: usage: broadcastcollation shardID, numCollations, collationSize, timeInMs",
			)
		}
		shardID, err := strconv.ParseInt(rpcArgs[0], 10, 64)
		if err != nil {
			log.Fatalf("wrong shard: %v", rpcArgs)
		}
		numCollations, err := strconv.Atoi(rpcArgs[1])
		if err != nil {
			log.Fatalf("wrong numCollations: %v", rpcArgs)
		}
		collationSize, err := strconv.Atoi(rpcArgs[2])
		if err != nil {
			log.Fatalf("wrong collationSize: %v", rpcArgs)
		}
		timeInMs, err := strconv.Atoi(rpcArgs[3])
		if err != nil {
			log.Fatalf("wrong timeInMs: %v", rpcArgs)
		}
		callRPCBroadcastCollation(
			rpcAddr,
			shardID,
			numCollations,
			collationSize,
			timeInMs,
		)
	} else {
		log.Fatalf("Client: wrong cmd '%v'", rpcCmd)
	}
}
