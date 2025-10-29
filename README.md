# chain-indexer

A Go library for indexing and monitoring EVM-compatible blockchain events. chain-indexer provides infrastructure for polling blocks, detecting reorganizations, and parsing smart contract event logs.

## Features

- **Blockchain Polling**: Configurable polling intervals for monitoring new blocks on EVM chains
- **Reorg Detection**: Automatic detection and reconciliation of blockchain reorganizations
- **Event Parsing**: Decode transaction logs using contract ABIs with support for multiple ABI versions
- **Batch Processing**: Efficient RPC batching with automatic chunking and retry logic
- **Pluggable Persistence**: Interface-based storage for block tracking with in-memory implementation included
- **Concurrent Operations**: Thread-safe operations with support for monitoring multiple chains

## Installation

```bash
go get github.com/Layr-Labs/chain-indexer
```

## Quick Start

```go
import (
    chainPoller "github.com/Layr-Labs/chain-indexer/pkg/chainPollers/evm"
    "github.com/Layr-Labs/chain-indexer/pkg/clients/ethereum"
    "github.com/Layr-Labs/chain-indexer/pkg/chainPollers/persistence/memory"
    "github.com/Layr-Labs/chain-indexer/pkg/config"
)

// Create an Ethereum client
ethClient := ethereum.NewEthereumClient(&ethereum.EthereumClientConfig{
    BaseUrl:   "https://your-rpc-endpoint",
    BlockType: ethereum.BlockType_Safe,
}, logger)

// Create persistence store
store := memory.NewInMemoryChainPollerPersistence()

// Implement IBlockHandler to process blocks and logs
type MyBlockHandler struct{}

func (h *MyBlockHandler) HandleBlock(ctx context.Context, block *ethereum.EthereumBlock) error {
    // Process block
    return nil
}

func (h *MyBlockHandler) HandleLog(ctx context.Context, logWithBlock *chainPoller.LogWithBlock) error {
    // Process decoded event log
    return nil
}

func (h *MyBlockHandler) HandleReorgBlock(ctx context.Context, blockNumber uint64) {
    // Handle reorg by invalidating data from this block
}

// Create and start the chain poller
poller := chainPoller.NewEVMChainPoller(
    ethClient,
    logParser,
    &chainPoller.EVMChainPollerConfig{
        ChainId:              config.ChainId(1),
        PollingInterval:      time.Second * 12,
        InterestingContracts: []string{"0xContractAddress"},
        MaxReorgDepth:        10,
        ReorgCheckEnabled:    true,
    },
    contractStore,
    store,
    &MyBlockHandler{},
    logger,
)

poller.Start(ctx)
```

## Development

### Prerequisites

- Go 1.24+
- Make

### Setup

```bash
# Install dependencies
make deps/go

# Run tests
make test

# Run linter
make lint

# Format code
make fmt

# Generate mocks
make mocks
```

### Running Tests

```bash
# Run all tests
make test

# Run specific package tests
go test -v -p 1 -parallel 1 ./pkg/chainPollers/evm
```

## Architecture

See [CLAUDE.md](CLAUDE.md) for detailed architecture documentation and development guidance.

## Disclaimer

**🚧 chain-indexer is under active development, and has not been audited.**

- Features may be added, removed, or modified
- Interfaces may have breaking changes
- Should be used **only for testing purposes** and **not in production**
- Provided "as is" without guarantee of functionality or production support

**Eigen Labs, Inc. does not provide support for production use.**

## Security

If you discover a vulnerability, please **do not** open an issue. Instead contact the maintainers directly at `security@eigenlabs.org`.
