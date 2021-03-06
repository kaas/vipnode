package ethnode

import (
	"context"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/rpc"
)

// NodeKind represents the different kinds of node implementations we know about.
type NodeKind int

const (
	Unknown NodeKind = iota // We'll treat unknown as Geth, just in case.
	Geth
	Parity
)

type NetworkID int

const (
	Mainnet NetworkID = 1
	Morden  NetworkID = 2
	Ropsten NetworkID = 3
	Rinkeby NetworkID = 4
	Kovan   NetworkID = 42
)

func (id NetworkID) String() string {
	switch id {
	case Mainnet:
		return "mainnet"
	case Morden:
		return "morden"
	case Ropsten:
		return "ropsten"
	case Rinkeby:
		return "rinkeby"
	case Kovan:
		return "kovan"
	}
	return "unknown"
}

// Is compares the ID to a network name.
func (id NetworkID) Is(network string) bool {
	return id.String() == strings.ToLower(network)
}

func (n NodeKind) String() string {
	switch n {
	case Geth:
		return "geth"
	case Parity:
		return "parity"
	default:
		return "unknown"
	}
}

// UserAgent is the metadata about node client.
type UserAgent struct {
	Version     string // Result of web3_clientVersion
	EthProtocol string // Result of eth_protocolVersion

	// Parsed/derived values
	Kind       NodeKind  // Node implementation
	Network    NetworkID // Network ID
	IsFullNode bool      // Is this a full node? (or a light client?)
}

// ParseUserAgent takes string values as output from the web3 RPC for
// web3_clientVersion, eth_protocolVersion, and net_version. It returns a
// parsed user agent metadata.
func ParseUserAgent(clientVersion, protocolVersion, netVersion string) (*UserAgent, error) {
	networkID, err := strconv.Atoi(netVersion)
	if err != nil {
		return nil, err
	}
	agent := &UserAgent{
		Version:     clientVersion,
		EthProtocol: protocolVersion,
		Network:     NetworkID(networkID),
		IsFullNode:  true,
	}
	if strings.HasPrefix(agent.Version, "Geth/") {
		agent.Kind = Geth
	} else if strings.HasPrefix(agent.Version, "Parity-Ethereum/") || strings.HasPrefix(agent.Version, "Parity/") {
		agent.Kind = Parity
	}

	protocol, err := strconv.ParseInt(protocolVersion, 0, 32)
	if err != nil {
		return nil, err
	}
	// FIXME: Can't find any docs on how this protocol value is supposed to be
	// parsed, so just using anecdotal values for now.
	if agent.Kind == Parity && protocol == 1 {
		agent.IsFullNode = false
	} else if agent.Kind == Geth && protocol == 10002 {
		agent.IsFullNode = false
	}
	return agent, nil
}

// Dial is a wrapper around go-ethereum/rpc.Dial with client detection.
func Dial(ctx context.Context, uri string) (EthNode, error) {
	client, err := rpc.DialContext(ctx, uri)
	if err != nil {
		return nil, err
	}

	return RemoteNode(client)
}

// DetectClient queries the RPC API to determine which kind of node is running.
func DetectClient(client *rpc.Client) (*UserAgent, error) {
	var clientVersion string
	if err := client.Call(&clientVersion, "web3_clientVersion"); err != nil {
		return nil, err
	}
	var protocolVersion string
	if err := client.Call(&protocolVersion, "eth_protocolVersion"); err != nil {
		return nil, err
	}
	var netVersion string
	if err := client.Call(&netVersion, "net_version"); err != nil {
		return nil, err
	}
	return ParseUserAgent(clientVersion, protocolVersion, netVersion)
}

// PeerInfo stores the node ID and client metadata about a peer.
type PeerInfo struct {
	ID   string `json:"id"`   // Unique node identifier (also the encryption pubkey)
	Name string `json:"name"` // Name of the node, including client type, version, OS, custom data
}

// EthNode is the normalized interface between different kinds of nodes.
type EthNode interface {
	ContractBackend() bind.ContractBackend

	// Kind returns the kind of node this is.
	Kind() NodeKind
	// Enode returns this node's enode://...
	Enode(ctx context.Context) (string, error)
	// AddTrustedPeer adds a nodeID to a set of nodes that can always connect, even
	// if the maximum number of connections is reached.
	AddTrustedPeer(ctx context.Context, nodeID string) error
	// RemoveTrustedPeer removes a nodeID from the trusted node set.
	RemoveTrustedPeer(ctx context.Context, nodeID string) error
	// ConnectPeer prompts a connection to the given nodeURI.
	ConnectPeer(ctx context.Context, nodeURI string) error
	// DisconnectPeer disconnects from the given nodeID, if connected.
	DisconnectPeer(ctx context.Context, nodeID string) error
	// Peers returns the list of connected peers
	Peers(ctx context.Context) ([]PeerInfo, error)
	// BlockNumber returns the current sync'd block number.
	BlockNumber(ctx context.Context) (uint64, error)
}

// RemoteNode autodetects the node kind and returns the appropriate EthNode
// implementation.
func RemoteNode(client *rpc.Client) (EthNode, error) {
	version, err := DetectClient(client)
	if err != nil {
		return nil, err
	}
	switch version.Kind {
	case Parity:
		return &parityNode{client: client}, nil
	default:
		// Treat everything else as Geth
		// FIXME: Is this a bad idea?
		node := &gethNode{client: client}
		ctx := context.TODO()
		if err := node.CheckCompatible(ctx); err != nil {
			return nil, err
		}
		return node, nil
	}
}
