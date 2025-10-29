# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

chain-indexer is a Go-based Ethereum blockchain indexing library designed for monitoring and parsing smart contract events. It provides infrastructure for polling EVM-compatible chains, detecting blockchain reorganizations, and decoding transaction logs using contract ABIs.

**Status**: Under active development, not audited, testing purposes only.

## Development Commands

### Dependencies
```bash
make deps/go              # Install all Go dependencies and tools
```

### Testing
```bash
make test                 # Run all tests with verbose output
go test -v -p 1 -parallel 1 ./pkg/path/to/package  # Run tests for specific package
```

### Code Quality
```bash
make lint                 # Run golangci-lint (5min timeout)
make fmt                  # Format all Go files with gofmt
make fmtcheck            # Check if files need formatting (fails if unformatted)
```

### Mocks
```bash
make mocks               # Generate test mocks using mockery
```

Mock generation is configured via `.mockery.yml` and outputs to `pkg/mocks/`.

## Architecture

### Core Components

**Chain Pollers** (`pkg/chainPollers/`)
- `IChainPoller`: Main interface for blockchain polling implementations
- `EVMChainPoller`: EVM-specific implementation that polls blocks at configurable intervals
- Handles blockchain reorganization detection by comparing parent hashes
- Configurable reorg depth and block history size
- Delegates block and log processing to `IBlockHandler` implementations

**Ethereum Client** (`pkg/clients/ethereum/`)
- Custom JSON-RPC client with retry logic and exponential backoff (1s to 60s)
- Supports both single calls and chunked batch calls (default 100 requests/batch)
- Provides methods for fetching blocks, logs, transactions, and receipts
- Block type selection: "safe" or "latest"

**Transaction Log Parser** (`pkg/transactionLogParser/`)
- Decodes Ethereum event logs using contract ABIs
- Extracts indexed parameters from topics and non-indexed from data field
- Handles multiple type conversions (integers, booleans, addresses, bytes, strings)
- Works with `ContractStore` to automatically fetch ABIs by address

**Contract Management** (`pkg/contracts/`, `pkg/contractStore/`)
- `Contract`: Represents smart contracts with name, address, ABI versions, and chain ID
- `IContractStore`: Interface for managing contract metadata and ABIs
- Supports combining multiple ABI versions for upgraded contracts
- In-memory implementation available at `pkg/contractStore/inMemoryContractStore/`

**Persistence** (`pkg/chainPollers/persistence/`)
- `IChainPollerPersistence`: Interface for storing processed block information
- Tracks last processed block to enable restarts without reprocessing
- In-memory implementation for testing at `pkg/chainPollers/persistence/memory/`
- Test suite (`test_suite.go`) for validating custom persistence implementations

### Data Flow

1. `EVMChainPoller.Start()` initializes from last processed block or chain head
2. Polling loop fetches new blocks at configured intervals
3. For each block, parent hash is validated against stored data (reorg detection)
4. If reorg detected, `reconcileReorg()` walks back to find common ancestor
5. Logs are fetched for "interesting contracts" (configured addresses)
6. `TransactionLogParser` decodes logs using contract ABIs from `ContractStore`
7. `IBlockHandler` receives decoded logs wrapped in `LogWithBlock` structures
8. Block metadata persisted via `IChainPollerPersistence`

### Key Interfaces to Implement

**IBlockHandler** (required for using EVMChainPoller)
- `HandleBlock(ctx, *EthereumBlock)`: Process new canonical blocks
- `HandleLog(ctx, *LogWithBlock)`: Process decoded event logs
- `HandleReorgBlock(ctx, blockNumber)`: Handle blocks invalidated by reorgs

**IChainPollerPersistence** (optional, in-memory default available)
- Store and retrieve processed block metadata
- Enable graceful restarts without reindexing
- Use `test_suite.go` to validate custom implementations

**IContractStore** (required for log decoding)
- Map contract addresses to ABIs
- Support contract upgrades via multiple ABI versions

## Testing Patterns

- Tests use testify assertions (`github.com/stretchr/testify`)
- Mocks generated with mockery/testify (`go.uber.org/mock` also available)
- Tests run sequentially (`-p 1 -parallel 1`) to avoid race conditions
- Persistence implementations should pass `pkg/chainPollers/persistence/test_suite.go`

## Important Considerations

- All contract addresses are normalized to lowercase for comparison
- Reorg detection compares parent hashes, not just block numbers
- Batch calls are chunked to avoid RPC provider limits
- Logs without topics (rare) are handled gracefully
- ABI parsing errors for duplicate receive/fallback functions are ignored
