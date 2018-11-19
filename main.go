package main

import (
	"context"
	"flag"
	"fmt"
	mrand "math/rand"
	"os"
	"strings"

	ds "github.com/ipfs/go-datastore"
	dsync "github.com/ipfs/go-datastore/sync"
	logging "github.com/ipfs/go-log"
	libp2p "github.com/libp2p/go-libp2p"
	crypto "github.com/libp2p/go-libp2p-crypto"
	ic "github.com/libp2p/go-libp2p-crypto"
	peer "github.com/libp2p/go-libp2p-peer"
	pstore "github.com/libp2p/go-libp2p-peerstore"
	rhost "github.com/libp2p/go-libp2p/p2p/host/routed"

	opentracing "github.com/opentracing/opentracing-go"

	jaeger "github.com/uber/jaeger-client-go"
	jaegerconfig "github.com/uber/jaeger-client-go/config"

	kaddht "github.com/libp2p/go-libp2p-kad-dht"
)

var logger = logging.Logger("sharding-p2p")

type (
	ShardIDType = int64
	PBInt       = int64
)

const (
	VersionMajor                  = 0
	VersionMinor                  = 0
	VersionPatch                  = 0
	numShards         ShardIDType = 100
	defaultListenPort             = 10000
	defaultRPCPort                = 13000
	defaultIP                     = "127.0.0.1"
)

func main() {
	// Parse options from the command line

	seed := flag.Int("seed", 0, "set random seed for id generation")
	listenIP := flag.String(
		"ip",
		defaultIP,
		"ip listened by the process for incoming connections",
	)
	listenPort := flag.Int(
		"port",
		defaultListenPort,
		"port listened by the node for incoming connections",
	)
	rpcIP := flag.String(
		"rpcip",
		defaultIP,
		"ip listened by the RPC server",
	)
	rpcPort := flag.Int("rpcport", defaultRPCPort, "RPC port listened by the RPC server")
	notifierPort := flag.Int(
		"notifierport",
		0,
		"notifier port listened by the event rpc server",
	)
	doBootstrapping := flag.Bool("bootstrap", false, "whether to do bootstrapping or not")
	bootnodesStr := flag.String("bootnodes", "", "multiaddresses of the bootnodes")
	logLevel := flag.String("loglevel", "INFO", "setting log level, e.g., DEBUG, WARNING, INFO, ERROR, CRITICAL")
	isClient := flag.Bool("client", false, "is RPC client or server")
	flag.Parse()

	rpcAddr := fmt.Sprintf("%v:%v", *rpcIP, *rpcPort)
	notifierAddr := fmt.Sprintf("%v:%v", *rpcIP, *notifierPort)
	logging.SetLogLevel("sharding-p2p", *logLevel)

	cliArgs := flag.Args()

	if len(cliArgs) > 0 && cliArgs[0] == "version" {
		version := fmt.Sprintf("%d.%d.%d", VersionMajor, VersionMinor, VersionPatch)
		fmt.Println(version)
		return
	}

	if *isClient {
		runClient(rpcAddr, cliArgs)
	} else {
		runServer(*listenIP, *listenPort, *seed, *doBootstrapping, *bootnodesStr, rpcAddr, notifierAddr)
	}
}

func runClient(rpcAddr string, cliArgs []string) {
	if len(cliArgs) <= 0 {
		logger.Fatal("Client: Invalid args")
		return
	}
	rpcCmd := cliArgs[0]
	rpcArgs := cliArgs[1:]
	switch rpcCmd {
	case "addpeer":
		doAddPeer(rpcArgs, rpcAddr)
	case "subshard":
		doSubShard(rpcArgs, rpcAddr)
	case "unsubshard":
		doUnsubShard(rpcArgs, rpcAddr)
	case "getsubshard":
		callRPCGetSubscribedShard(rpcAddr)
	case "broadcastcollation":
		doBroadcastCollation(rpcArgs, rpcAddr)
	case "stop":
		callRPCStopServer(rpcAddr)
	case "listpeer":
		callRPCListPeer(rpcAddr)
	case "listtopicpeer":
		doListTopicPeer(rpcArgs, rpcAddr)
	case "listshardpeer":
		doListShardPeer(rpcArgs, rpcAddr)
	case "removepeer":
		doRemovePeer(rpcArgs, rpcAddr)
	default:
		logger.Fatalf("Client: Invalid cmd '%v'", rpcCmd)
	}
}

func runServer(
	listenIP string,
	listenPort int,
	seed int,
	doBootstrapping bool,
	bootnodesStr string,
	rpcAddr string,
	notifierAddr string) {
	ctx := context.Background()
	var eventNotifier EventNotifier
	var err error
	// Check if notifier port is 0 and use mock event notifier if so
	if strings.Split(notifierAddr, ":")[1] == "0" {
		eventNotifier = NewMockEventNotifier()
	} else {
		eventNotifier, err = NewRpcEventNotifier(ctx, notifierAddr)
		if err != nil {
			logger.Fatalf("Failed to connect RPC event notifier: %v", err)
		}
	}
	var bootnodes = []pstore.PeerInfo{}
	if bootnodesStr != "" {
		bootnodes, err = convertPeers(strings.Split(bootnodesStr, ","))
		if err != nil {
			logger.Errorf("Failed to convert bootnode address: %v, to peer info format, err: %v", bootnodesStr, err)
		}
	}

	node, err := makeNode(
		ctx,
		listenIP,
		listenPort,
		seed,
		eventNotifier,
		doBootstrapping,
		bootnodes,
	)
	if err != nil {
		logger.Fatalf("Failed to make node, err: %v", err)
	}

	// Set up Opentracing and Jaeger tracer
	var localAgentHostPort string
	if (os.Getenv("JAEGER_AGENT_HOST") != "") && (os.Getenv("JAEGER_AGENT_PORT") != "") {
		localAgentHostPort = fmt.Sprintf("%v:%v", os.Getenv("JAEGER_AGENT_HOST"), os.Getenv("JAEGER_AGENT_PORT"))
	}
	cfg := &jaegerconfig.Configuration{
		Sampler: &jaegerconfig.SamplerConfig{
			Type:  "const",
			Param: 1,
		},
		Reporter: &jaegerconfig.ReporterConfig{
			LogSpans:           true,
			LocalAgentHostPort: localAgentHostPort,
		},
	}
	var tracerName string
	if os.Getenv("NODE_NAME") != "" {
		tracerName = fmt.Sprintf("%v's RPC server", os.Getenv("NODE_NAME"))
	} else {
		tracerName = fmt.Sprintf("RPC Server@%v", rpcAddr)
	}
	tracer, closer, err := cfg.New(tracerName, jaegerconfig.Logger(jaeger.StdLogger))
	if err != nil {
		logger.Debugf("Failed to create tracer, err: %v", err)
	} else {
		opentracing.SetGlobalTracer(tracer)
		defer closer.Close()
	}
	// End of tracer setup

	logger.Infof(
		"Node is listening: seed=%v, addr=%v, peerID=%v",
		seed,
		fmt.Sprintf("%v:%v", listenIP, listenPort),
		node.ID().Pretty(),
	)
	runRPCServer(node, rpcAddr)
}

func makeKey(seed int) (ic.PrivKey, peer.ID, error) {
	// If the seed is zero, use real cryptographic randomness. Otherwise, use a
	// deterministic randomness source to make generated keys stay the same
	// across multiple runs
	r := mrand.New(mrand.NewSource(int64(seed)))
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

func makeNode(
	ctx context.Context,
	listenIP string,
	listenPort int,
	randseed int,
	eventNotifier EventNotifier,
	doBootstrapping bool,
	bootstrapPeers []pstore.PeerInfo) (*Node, error) {
	listenAddrString := fmt.Sprintf("/ip4/%v/tcp/%v", listenIP, listenPort)

	priv, _, err := makeKey(randseed)
	if err != nil {
		return nil, err
	}

	basicHost, err := libp2p.New(
		ctx,
		libp2p.Identity(priv),
		libp2p.ListenAddrStrings(listenAddrString),
		libp2p.DisableRelay(),
	)
	if err != nil {
		return nil, err
	}

	// Construct a datastore (needed by the DHT). This is just a simple, in-memory thread-safe datastore.
	dstore := dsync.MutexWrap(ds.NewMapDatastore())

	// Make the DHT
	dht := kaddht.NewDHT(ctx, basicHost, dstore)

	// Make the routed host
	routedHost := rhost.Wrap(basicHost, dht)

	// Make a host that listens on the given multiaddress
	node := NewNode(ctx, routedHost, dht, eventNotifier)
	if doBootstrapping {
		node.StartBootstrapping(ctx, bootstrapPeers)
	}

	return node, nil
}
