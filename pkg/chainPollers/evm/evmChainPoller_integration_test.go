package EVMChainPoller

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Layr-Labs/chain-indexer/pkg/chainPollers/contractRegistry"
	"github.com/Layr-Labs/chain-indexer/pkg/chainPollers/persistence/memory"
	"github.com/Layr-Labs/chain-indexer/pkg/clients/ethereum"
	"github.com/Layr-Labs/chain-indexer/pkg/config"
	"github.com/Layr-Labs/chain-indexer/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	baseSepoliaChainId = config.ChainId(84532)

	// L2 predeploy contracts (exist on both Base mainnet and Sepolia)
	l2StandardBridge       = "0x4200000000000000000000000000000000000010"
	l2ToL1MessagePasser    = "0x4200000000000000000000000000000000000016"
	l2CrossDomainMessenger = "0x4200000000000000000000000000000000000007"
	wethPredeploy          = "0x4200000000000000000000000000000000000006"

	// Wide range to increase odds of catching events on testnet
	integrationFromBlock = uint64(37_000_000)
	integrationToBlock   = uint64(37_000_100)

	// Single block for poller-level tests
	integrationSingleBlock = uint64(37_000_050)

	// Pinned factory-pattern test data (from successful run on 2026-02-10).
	// Block 37,482,030 has 2 L2StandardBridge events referencing the OP Stack
	// native ETH sentinel address as l2Token in topics[2].
	factoryBlock        = uint64(37_482_030)
	factoryChildAddress = "0xdeaddeaddeaddeaddeaddeaddeaddeaddead0000"
)

func skipIfNoRPC(t *testing.T) string {
	t.Helper()
	rpcURL := os.Getenv("BASE_RPC_URL")
	if rpcURL == "" {
		t.Skip("BASE_RPC_URL not set, skipping integration test")
	}
	return rpcURL
}

func newBaseClient(t *testing.T, rpcURL string) ethereum.Client {
	t.Helper()
	l, err := logger.NewLogger(&logger.LoggerConfig{Debug: true})
	require.NoError(t, err)
	return ethereum.NewEthereumClient(&ethereum.EthereumClientConfig{
		BaseUrl:   rpcURL,
		BlockType: ethereum.BlockType_Latest,
	}, l)
}

func TestIntegration_GetLogsBatch_BaseSepolia(t *testing.T) {
	rpcURL := skipIfNoRPC(t)
	client := newBaseClient(t, rpcURL)

	addresses := []string{
		l2StandardBridge,
		l2ToL1MessagePasser,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	logs, err := client.GetLogsBatch(ctx, addresses, integrationFromBlock, integrationToBlock)
	require.NoError(t, err)
	assert.NotNil(t, logs)

	t.Logf("Fetched %d logs across %d blocks for %d addresses",
		len(logs), integrationToBlock-integrationFromBlock+1, len(addresses))

	for _, log := range logs {
		blockNum := log.BlockNumber.Value()
		assert.True(t, blockNum >= integrationFromBlock && blockNum <= integrationToBlock,
			"log block %d outside requested range [%d, %d]", blockNum, integrationFromBlock, integrationToBlock)
	}
}

func TestIntegration_FetchLogs_WithDynamicRegistry_BaseSepolia(t *testing.T) {
	rpcURL := skipIfNoRPC(t)
	l, err := logger.NewLogger(&logger.LoggerConfig{Debug: true})
	require.NoError(t, err)

	client := newBaseClient(t, rpcURL)
	registry := contractRegistry.NewInMemoryContractRegistry()
	store := memory.NewInMemoryChainPollerPersistence()

	poller := &EVMChainPoller{
		ethClient: client,
		store:     store,
		logger:    l,
		config: &EVMChainPollerConfig{
			ChainId:                    baseSepoliaChainId,
			InterestingContracts:       []string{l2StandardBridge},
			MaxAddressesPerLogsRequest: 1000,
			LogsFetchTimeout:           60 * time.Second,
		},
		contractRegistry: registry,
	}

	logs1, err := poller.fetchLogsForInterestingContractsForBlock(integrationSingleBlock)
	require.NoError(t, err)
	t.Logf("Static only (L2StandardBridge): %d logs", len(logs1))

	registry.RegisterContracts([]string{l2ToL1MessagePasser, wethPredeploy})

	contracts := poller.listAllInterestingContracts()
	t.Logf("Contracts after dynamic registration: %v", contracts)
	assert.Len(t, contracts, 3)

	logs2, err := poller.fetchLogsForInterestingContractsForBlock(integrationSingleBlock)
	require.NoError(t, err)
	t.Logf("Static + dynamic (3 contracts): %d logs", len(logs2))

	assert.GreaterOrEqual(t, len(logs2), len(logs1),
		"adding dynamic contracts should return >= logs")
}

func TestIntegration_BatchConcurrency_BaseSepolia(t *testing.T) {
	rpcURL := skipIfNoRPC(t)
	l, err := logger.NewLogger(&logger.LoggerConfig{Debug: true})
	require.NoError(t, err)

	client := newBaseClient(t, rpcURL)
	store := memory.NewInMemoryChainPollerPersistence()

	poller := &EVMChainPoller{
		ethClient: client,
		store:     store,
		logger:    l,
		config: &EVMChainPollerConfig{
			ChainId: baseSepoliaChainId,
			InterestingContracts: []string{
				l2StandardBridge,
				l2ToL1MessagePasser,
				l2CrossDomainMessenger,
				wethPredeploy,
			},
			MaxAddressesPerLogsRequest: 2, // force 2 concurrent batches of 2 addresses
			LogsFetchTimeout:           60 * time.Second,
		},
	}

	logs, err := poller.fetchLogsForInterestingContractsForBlock(integrationSingleBlock)
	require.NoError(t, err)
	t.Logf("Multi-batch fetch: %d logs from 4 predeploys in 2 concurrent batches", len(logs))
}

// TestIntegration_FactoryPattern_BaseSepolia simulates the full factory-to-child
// indexing flow against a real Base Sepolia RPC using pinned on-chain data:
//
//	Block 37,482,030 has 2 L2StandardBridge events. The bridge event topics[2]
//	references 0xdead...0000 (OP Stack native ETH sentinel) as the l2Token.
//	This simulates a factory emitting an event that references a child contract.
//
// Flow:
//  1. Watch only the L2StandardBridge ("factory")
//  2. Fetch logs at the pinned block -- confirms bridge has events
//  3. Register the known child address (simulating handler discovery)
//  4. Fetch again and confirm the child is now included in the query
func TestIntegration_FactoryPattern_BaseSepolia(t *testing.T) {
	rpcURL := skipIfNoRPC(t)
	l, err := logger.NewLogger(&logger.LoggerConfig{Debug: true})
	require.NoError(t, err)

	client := newBaseClient(t, rpcURL)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Verify the pinned block still has the expected bridge events
	bridgeLogs, err := client.GetLogsBatch(ctx, []string{l2StandardBridge}, factoryBlock, factoryBlock)
	require.NoError(t, err)
	require.NotEmpty(t, bridgeLogs, "pinned block %d should have L2StandardBridge events", factoryBlock)
	t.Logf("Pinned block %d has %d bridge events", factoryBlock, len(bridgeLogs))

	// Step 1: Create poller watching only the "factory" (bridge)
	registry := contractRegistry.NewInMemoryContractRegistry()
	store := memory.NewInMemoryChainPollerPersistence()

	poller := &EVMChainPoller{
		ethClient: client,
		store:     store,
		logger:    l,
		config: &EVMChainPollerConfig{
			ChainId:                    baseSepoliaChainId,
			InterestingContracts:       []string{l2StandardBridge},
			MaxAddressesPerLogsRequest: 1000,
			LogsFetchTimeout:           60 * time.Second,
		},
		contractRegistry: registry,
	}

	// Step 2: Fetch logs -- only bridge is watched
	contractsBefore := poller.listAllInterestingContracts()
	assert.Len(t, contractsBefore, 1, "should only have the bridge before registration")

	logs1, err := poller.fetchLogsForInterestingContractsForBlock(factoryBlock)
	require.NoError(t, err)
	assert.NotEmpty(t, logs1, "bridge should have logs at pinned block")
	t.Logf("Before registration (bridge only): %d logs at block %d", len(logs1), factoryBlock)

	// Step 3: Factory handler "discovers" the child and registers it
	t.Logf("Simulated factory handler: discovered child %s at block %d", factoryChildAddress, factoryBlock)
	registry.RegisterContracts([]string{factoryChildAddress})

	contractsAfter := poller.listAllInterestingContracts()
	assert.Len(t, contractsAfter, 2, "should have bridge + child after registration")
	t.Logf("Registered contracts: %v", contractsAfter)

	// Step 4: Fetch logs again -- now includes the child contract
	logs2, err := poller.fetchLogsForInterestingContractsForBlock(factoryBlock)
	require.NoError(t, err)
	t.Logf("After registration (bridge + child): %d logs at block %d", len(logs2), factoryBlock)

	assert.GreaterOrEqual(t, len(logs2), len(logs1),
		"adding the discovered child should return >= logs")

	// Step 5: Cross-verify by fetching child logs directly
	childLogsDirectly, err := client.GetLogsBatch(ctx, []string{factoryChildAddress}, factoryBlock, factoryBlock)
	require.NoError(t, err)
	t.Logf("Direct child-only fetch: %d logs at block %d", len(childLogsDirectly), factoryBlock)

	if len(childLogsDirectly) > 0 {
		assert.Greater(t, len(logs2), len(logs1),
			"child had logs at this block, so merged result should be strictly greater")
	}
}
