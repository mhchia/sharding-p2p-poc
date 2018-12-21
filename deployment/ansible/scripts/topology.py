import sys
import random
from copy import deepcopy
import yaml
from collections import defaultdict

RPC_PORT_GAP = 100

# broadcast collation parameters
NUM_COLLATIONS = 10
COLLATION_SIZE = 100
FREQUENCY = 50


class Peer:
    def __init__(self, host, _id):
        self.container_name = f"peer_{_id}"
        self.host = host
        self.public_ip = host.split("@")[1]
        self.listen_port = _id
        self.rpc_port = _id + RPC_PORT_GAP
        self.seed = _id

    def __repr__(self):
        return f"<{self.container_name}: {self.public_ip}, l:{self.listen_port}, r:{self.rpc_port}, s:{self.seed}>"


def parse_inventory(line):
    host, _, _start, _end = line.strip().split(" ")
    start = int(_start.split("=")[1])
    end = int(_end.split("=")[1])
    return host, start, end


def hosts_to_peers(lines):
    peers = []
    for line in lines:
        host, start, end = parse_inventory(line)
        for i in range(start, end + 1):
            peer = Peer(host, i)
            peers.append(peer)
    return peers


def read_inventories(path):
    with open(path, "r") as f:
        lines = f.readlines()
    inventories = defaultdict(list)
    for _line in lines:
        line = _line.strip()
        if not line.startswith("["):
            inventories[key].append(line)
        else:
            key = line
    return inventories


def barbell_topology(_peers):
    peers = deepcopy(_peers)
    random.seed(5566)
    random.shuffle(peers)
    n = len(peers)
    topology = [{
        'peer': peer,
        'connect_to': [peers[(i + 1)]] if i + 1 < n else [],
        'shard_id': int(i % 3),
        'is_broadcasting': i == 0
    } for i, peer in enumerate(peers)]

    return topology


def groupby_host(commands, hosts):
    groups = {host: [] for host in hosts}
    for item in commands:
        groups[item["host"]].append(item["command"])
    return groups


def to_addpeer_commands(topology):
    commands = [
        {
            "host": item["peer"].host,
            "command": (
                f"docker exec -t {item['peer'].container_name} sh -c "
                f"'./sharding-p2p-poc -verbose "
                f"-client addpeer {other.public_ip} {other.listen_port} {other.seed}'"
            )
        }
        for item in topology
        for other in item["connect_to"]
    ]

    return commands


def to_subshard_commands(topology):
    commands = [
        {
            "host": item["peer"].host,
            "command": (
                f"docker exec -t {item['peer'].container_name} sh -c "
                f"'./sharding-p2p-poc -verbose "
                f"-client subshard {item['shard_id']}'")
        }
        for item in topology
    ]
    return commands


def to_broadcastcollation_commands(topology):
    commands = [
        {
            "host": item["peer"].host,
            "command": (
                f"docker exec -t {item['peer'].container_name} sh -c "
                f"'./sharding-p2p-poc -verbose "
                f"-client broadcastcollation {item['shard_id']} {NUM_COLLATIONS} {COLLATION_SIZE} {FREQUENCY}'"
            )
        }
        for item in topology if item["is_broadcasting"]
    ]
    return commands

def visualize(topology):
    import networkx as nx
    G = nx.Graph()
    for item in topology:
        me = item["peer"].container_name
        G.add_node(me, shard_id = item["shard_id"])
        for other in item['connect_to']:
            G.add_edge(me, other.container_name)
    import matplotlib.pyplot as plt
    pos_nodes = nx.spring_layout(G)
    nx.draw(G, pos_nodes, with_labels=True)

    pos_attrs = {node: (coords[0], coords[1] + 0.08) for node, coords in pos_nodes.items()}

    node_attrs = nx.get_node_attributes(G, 'shard_id')
    custom_node_attrs = {node: attr for node, attr in node_attrs.items()}
    nx.draw_networkx_labels(G, pos_attrs, labels=custom_node_attrs)

    plt.savefig("artifacts/network.png")


if __name__ == '__main__':
    inventories = read_inventories(sys.argv[1])
    nodes = inventories["[nodes]"]
    hosts = list(map(lambda x: x.split(" ")[0], nodes))
    print(hosts)
    peers = hosts_to_peers(nodes)

    print("\n--- [ Network Nodes ] ---\n")

    for peer in peers:
        print(peer.container_name, peer.public_ip)

    topology = barbell_topology(peers)
    functions = {
        "addpeer": to_addpeer_commands,
        "subshard": to_subshard_commands,
        "broadcastcollation": to_broadcastcollation_commands
    }
    results = {
        k: groupby_host(v(topology), hosts)
        for k, v in functions.items()
    }

    yml = yaml.dump(results, default_flow_style=False, width=1000)

    print("\n--- [ Commands ] ---\n")
    print(yml)
    with open("artifacts/commands.yml", "w") as f:
        f.write(yml)

    visualize(topology)
