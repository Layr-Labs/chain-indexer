package EVMChainPoller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	chainPoller "github.com/Layr-Labs/chain-indexer/pkg/chainPollers"
	"github.com/Layr-Labs/chain-indexer/pkg/chainPollers/persistence"
	"github.com/Layr-Labs/chain-indexer/pkg/clients/ethereum"
	"github.com/Layr-Labs/chain-indexer/pkg/config"
	"github.com/Layr-Labs/chain-indexer/pkg/contractStore"
	"github.com/Layr-Labs/chain-indexer/pkg/transactionLogParser"
	"go.uber.org/zap"
)

const (
	DefaultMaxAddressesPerLogsRequest = 1000
	DefaultLogsFetchTimeout           = 60 * time.Second
)

type EVMChainPollerConfig struct {
	ChainId              config.ChainId
	PollingInterval      time.Duration
	InterestingContracts []string
	AvsAddress           string

	MaxReorgDepth     int
	BlockHistorySize  int
	ReorgCheckEnabled bool

	MaxAddressesPerLogsRequest int
	LogsFetchTimeout           time.Duration
}

type EVMChainPoller struct {
	ethClient        ethereum.Client
	logParser        transactionLogParser.LogParser
	config           *EVMChainPollerConfig
	contractStore    contractStore.IContractStore
	logger           *zap.Logger
	store            chainPoller.IChainPollerPersistence
	blockHandler     chainPoller.IBlockHandler
	contractRegistry chainPoller.ContractRegistry
}

type EVMChainPollerOption func(*EVMChainPoller)

func WithContractRegistry(r chainPoller.ContractRegistry) EVMChainPollerOption {
	return func(ecp *EVMChainPoller) {
		ecp.contractRegistry = r
	}
}

func NewEVMChainPoller(
	ethClient ethereum.Client,
	logParser transactionLogParser.LogParser,
	config *EVMChainPollerConfig,
	contractStore contractStore.IContractStore,
	store chainPoller.IChainPollerPersistence,
	blockHandler chainPoller.IBlockHandler,
	logger *zap.Logger,
	opts ...EVMChainPollerOption,
) *EVMChainPoller {

	if store == nil {
		panic("store is required")
	}

	if config.MaxReorgDepth == 0 {
		config.MaxReorgDepth = 10
	}
	if config.BlockHistorySize == 0 {
		config.BlockHistorySize = 100
	}
	if !config.ReorgCheckEnabled && config.MaxReorgDepth > 0 {
		config.ReorgCheckEnabled = true
	}
	if config.MaxAddressesPerLogsRequest == 0 {
		config.MaxAddressesPerLogsRequest = DefaultMaxAddressesPerLogsRequest
	}
	if config.LogsFetchTimeout == 0 {
		config.LogsFetchTimeout = DefaultLogsFetchTimeout
	}

	for i, contract := range config.InterestingContracts {
		logger.Sugar().Infof("InterestingContracts %d: %s\n", i, contract)
	}
	pollerLogger := logger.With(
		zap.Uint("chainId", uint(config.ChainId)),
	)
	ecp := &EVMChainPoller{
		ethClient:     ethClient,
		logger:        pollerLogger,
		logParser:     logParser,
		config:        config,
		contractStore: contractStore,
		store:         store,
		blockHandler:  blockHandler,
	}
	for _, opt := range opts {
		opt(ecp)
	}
	return ecp
}

func (ecp *EVMChainPoller) Start(ctx context.Context) error {

	ecp.logger.Sugar().Infow("Starting Ethereum Listener",
		zap.Any("chainId", ecp.config.ChainId),
		zap.Duration("pollingInterval", ecp.config.PollingInterval),
	)

	lastBlockRecord, err := ecp.store.GetLastProcessedBlock(ctx, ecp.config.ChainId)

	if err != nil {
		ecp.logger.Sugar().Infow("Poller could not get last processed block so using latest block")
		block, err := ecp.ethClient.GetLatestBlock(ctx)
		if err != nil {
			return fmt.Errorf("error getting latest block: %w", err)
		}

		lastCanonBlock, err := ecp.ethClient.GetBlockByNumber(ctx, block)
		if err != nil {
			return fmt.Errorf("couldn't get last canonical block: %w", err)
		}

		lastBlockRecord = &chainPoller.BlockRecord{
			Number:     lastCanonBlock.Number.Value(),
			Hash:       lastCanonBlock.Hash.Value(),
			ParentHash: lastCanonBlock.ParentHash.Value(),
			Timestamp:  lastCanonBlock.Timestamp.Value(),
			ChainId:    ecp.config.ChainId,
		}

		err = ecp.store.SaveBlock(ctx, lastBlockRecord)
		if err != nil {
			return fmt.Errorf("failed to save last processed block: %w", err)
		}
	}

	if lastBlockRecord == nil {
		return fmt.Errorf("last processed block must exist")
	}

	go ecp.pollForBlocks(ctx)

	return nil
}

func (ecp *EVMChainPoller) pollForBlocks(ctx context.Context) {

	ecp.logger.Sugar().Infow("Starting Ethereum Chain Listener poll loop")
	ticker := time.NewTicker(ecp.config.PollingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			ecp.logger.Sugar().Infow("Polling loop context cancelled, stopping")
			return
		case <-ticker.C:
			ecp.processNextBlock(ctx)
		}
	}
}

func (ecp *EVMChainPoller) processNextBlock(ctx context.Context) {

	latestBlockRecord, err := ecp.store.GetLastProcessedBlock(ctx, ecp.config.ChainId)
	if err != nil {
		ecp.logger.Sugar().Errorw("Error getting last processed block", "error", err)
		return
	}

	latestBlockNum, err := ecp.ethClient.GetLatestBlock(ctx)
	if err != nil {
		ecp.logger.Sugar().Errorw("Error getting latest block number", "error", err)
		return
	}

	if latestBlockRecord.Number == latestBlockNum {
		ecp.logger.Sugar().Debugw("Skipping block processing as the last observed block is the same as the latest block",
			zap.Uint64("lastObservedBlock", latestBlockRecord.Number),
			zap.Uint64("latestBlock", latestBlockNum),
		)
		return
	}

	var blocksToFetch []uint64
	if latestBlockNum > latestBlockRecord.Number {
		for i := latestBlockRecord.Number + 1; i <= latestBlockNum; i++ {
			blocksToFetch = append(blocksToFetch, i)
		}
	}

	ecp.logger.Sugar().Debugw("Fetching blocks with logs",
		zap.Any("blocksToFetch", blocksToFetch),
	)

	for _, blockNum := range blocksToFetch {
		newCanonBlock, err := ecp.ethClient.GetBlockByNumber(ctx, blockNum)
		if err != nil {
			ecp.logger.Sugar().Errorw("Failed to fetch block for reorg check",
				zap.Uint64("blockNumber", blockNum),
				zap.Error(err),
			)
			return
		}

		if newCanonBlock.ParentHash.Value() != latestBlockRecord.Hash {
			ecp.logger.Sugar().Warnw("Blockchain reorganization detected",
				"blockNumber", blockNum,
				"expectedParent", latestBlockRecord.Hash,
				"actualParent", newCanonBlock.ParentHash.Value(),
				"chainId", ecp.config.ChainId)

			if err = ecp.reconcileReorg(ctx, newCanonBlock); err != nil {
				ecp.logger.Sugar().Errorw("Failed to reconcile reorg", "error", err)
			}
			return
		}

		if err := ecp.blockHandler.HandleBlock(ctx, newCanonBlock); err != nil {
			ecp.logger.Sugar().Errorw("Error handling new block",
				zap.Uint64("blockNumber", blockNum),
				zap.Error(err),
			)
		}

		latestBlockRecord, err = ecp.processBlockLogs(ctx, newCanonBlock)
		if err != nil {
			ecp.logger.Sugar().Errorw("Error fetching block with logs",
				zap.Uint64("blockNumber", blockNum),
				zap.Error(err),
			)
			return
		}
	}

	ecp.logger.Sugar().Debugw("All blocks processed", zap.Any("blocksToFetch", blocksToFetch))

	if len(blocksToFetch) > 0 && blocksToFetch[len(blocksToFetch)-1]%100 == 0 {
		ecp.logger.Sugar().Infow("Processed block",
			zap.Uint64("blockNumber", blocksToFetch[len(blocksToFetch)-1]),
		)
	}
}

func (ecp *EVMChainPoller) processBlockLogs(ctx context.Context, block *ethereum.EthereumBlock) (*chainPoller.BlockRecord, error) {
	logs, err := ecp.fetchLogsForInterestingContractsForBlock(block.Number.Value())
	if err != nil {
		ecp.logger.Sugar().Errorw("Error fetching logs for block",
			zap.Uint64("blockNumber", block.Number.Value()),
			zap.Error(err),
		)
		return nil, err
	}

	block.ChainId = ecp.config.ChainId

	ecp.logger.Sugar().Infow("Block fetched with logs",
		"latestBlockNum", block.Number.Value(),
		"blockHash", block.Hash.Value(),
		"logCount", len(logs),
	)

	for _, log := range logs {
		decodedLog, err := ecp.logParser.DecodeLog(nil, log)
		if err != nil {
			ecp.logger.Sugar().Errorw("Failed to decode log",
				zap.String("transactionHash", log.TransactionHash.Value()),
				zap.String("logAddress", log.Address.Value()),
				zap.Uint64("logIndex", log.LogIndex.Value()),
				zap.Error(err),
			)
			return nil, err
		}

		lwb := &chainPoller.LogWithBlock{
			Block:  block,
			RawLog: log,
			Log:    decodedLog,
		}
		if err = ecp.blockHandler.HandleLog(ctx, lwb); err != nil {
			return nil, err
		}
	}
	ecp.logger.Sugar().Debugw("Processed logs",
		zap.Uint64("blockNumber", block.Number.Value()),
	)

	blockRecord := &chainPoller.BlockRecord{
		Number:     block.Number.Value(),
		Hash:       block.Hash.Value(),
		ParentHash: block.ParentHash.Value(),
		Timestamp:  block.Timestamp.Value(),
		ChainId:    ecp.config.ChainId,
	}

	if err = ecp.store.SaveBlock(ctx, blockRecord); err != nil {
		ecp.logger.Sugar().Warnw("Failed to save block info",
			"error", err,
			"blockNumber", blockRecord.Number)
	}

	if ecp.config.BlockHistorySize > 0 && blockRecord.Number > uint64(ecp.config.BlockHistorySize) {
		oldBlockNum := blockRecord.Number - uint64(ecp.config.BlockHistorySize)
		if err := ecp.store.DeleteBlock(ctx, ecp.config.ChainId, oldBlockNum); err != nil {
			ecp.logger.Sugar().Debugw("Failed to prune old block",
				"blockNumber", oldBlockNum,
				"error", err)
			// TODO: non-fatal for now. Does run the (low) risk of orphaned storage usage growth
		}
	}

	return blockRecord, nil
}

func (ecp *EVMChainPoller) ContractRegistry() chainPoller.ContractRegistry {
	return ecp.contractRegistry
}

func (ecp *EVMChainPoller) listAllInterestingContracts() []string {
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, contract := range ecp.config.InterestingContracts {
		if contract == "" {
			continue
		}
		lower := strings.ToLower(contract)
		if _, ok := seen[lower]; !ok {
			seen[lower] = struct{}{}
			result = append(result, lower)
		}
	}
	if ecp.contractRegistry != nil {
		for _, addr := range ecp.contractRegistry.ListContracts() {
			if _, ok := seen[addr]; !ok {
				seen[addr] = struct{}{}
				result = append(result, addr)
			}
		}
	}
	return result
}

func chunkAddresses(addresses []string, chunkSize int) [][]string {
	if chunkSize <= 0 {
		chunkSize = DefaultMaxAddressesPerLogsRequest
	}
	chunks := make([][]string, 0, (len(addresses)+chunkSize-1)/chunkSize)
	for i := 0; i < len(addresses); i += chunkSize {
		end := i + chunkSize
		if end > len(addresses) {
			end = len(addresses)
		}
		chunks = append(chunks, addresses[i:end])
	}
	return chunks
}

func (ecp *EVMChainPoller) fetchLogsForInterestingContractsForBlock(blockNumber uint64) ([]*ethereum.EthereumEventLog, error) {
	ctxWithTimeout, cancel := context.WithTimeout(context.Background(), ecp.config.LogsFetchTimeout)
	defer cancel()

	allContracts := ecp.listAllInterestingContracts()
	if len(allContracts) == 0 {
		return []*ethereum.EthereumEventLog{}, nil
	}

	ecp.logger.Sugar().Infow("Fetching logs for interesting contracts",
		zap.Int("contractCount", len(allContracts)),
		zap.Uint64("blockNumber", blockNumber),
	)

	batches := chunkAddresses(allContracts, ecp.config.MaxAddressesPerLogsRequest)

	var wg sync.WaitGroup
	logResultsChan := make(chan []*ethereum.EthereumEventLog, len(batches))
	errorsChan := make(chan error, len(batches))

	for i, batch := range batches {
		wg.Add(1)
		go func(batchIdx int, addrs []string) {
			defer wg.Done()

			ecp.logger.Sugar().Debugw("Fetching logs batch",
				zap.Int("batchIdx", batchIdx),
				zap.Int("addressCount", len(addrs)),
				zap.Uint64("blockNumber", blockNumber),
			)

			logs, err := ecp.ethClient.GetLogsBatch(ctxWithTimeout, addrs, blockNumber, blockNumber)
			if err != nil {
				ecp.logger.Sugar().Errorw("Failed to fetch logs batch",
					zap.Int("batchIdx", batchIdx),
					zap.Uint64("blockNumber", blockNumber),
					zap.Error(err),
				)
				errorsChan <- fmt.Errorf("failed to fetch logs batch %d: %w", batchIdx, err)
				return
			}

			ecp.logger.Sugar().Debugw("Fetched logs batch",
				zap.Int("batchIdx", batchIdx),
				zap.Uint64("blockNumber", blockNumber),
				zap.Int("logCount", len(logs)),
			)

			logResultsChan <- logs
		}(i, batch)
	}

	wg.Wait()
	close(logResultsChan)
	close(errorsChan)

	allErrors := make([]error, 0)
	for err := range errorsChan {
		allErrors = append(allErrors, err)
	}
	if len(allErrors) > 0 {
		return nil, fmt.Errorf("failed to fetch logs for contracts: %v", allErrors)
	}

	allLogs := make([]*ethereum.EthereumEventLog, 0)
	for batchLogs := range logResultsChan {
		allLogs = append(allLogs, batchLogs...)
	}

	ecp.logger.Sugar().Infow("All logs fetched",
		zap.Uint64("blockNumber", blockNumber),
		zap.Int("logCount", len(allLogs)),
	)

	return allLogs, nil
}

func (ecp *EVMChainPoller) reconcileReorg(ctx context.Context, startBlock *ethereum.EthereumBlock) error {
	orphanedBlocks, err := ecp.findOrphanedBlocks(ctx, startBlock, ecp.config.MaxReorgDepth)

	if err != nil {
		return err
	}

	if len(orphanedBlocks) == 0 {
		return fmt.Errorf("no orphaned blocks found")
	}

	for _, orphanedBlock := range orphanedBlocks {
		ecp.blockHandler.HandleReorgBlock(ctx, orphanedBlock.Number)

		err = ecp.store.DeleteBlock(ctx, orphanedBlock.ChainId, orphanedBlock.Number)
		if err != nil && !errors.Is(err, persistence.ErrNotFound) {
			return fmt.Errorf("failed to delete orphaned block: %w", err)
		}
	}

	return nil
}

func (ecp *EVMChainPoller) findOrphanedBlocks(ctx context.Context, startBlock *ethereum.EthereumBlock, maxDepth int) ([]*chainPoller.BlockRecord, error) {
	var parentBlockRecord *chainPoller.BlockRecord
	var orphanedBlocks []*chainPoller.BlockRecord
	startBlockNumber := startBlock.Number.Value()

	for parentBlockNum := startBlockNumber - 1; startBlockNumber-parentBlockNum <= uint64(maxDepth) && parentBlockNum > 0; parentBlockNum-- {

		canonParentBlock, err := ecp.ethClient.GetBlockByNumber(ctx, parentBlockNum)
		if err != nil || canonParentBlock == nil {
			return nil, fmt.Errorf("failed to fetch block %d from chain: %w", parentBlockNum, err)
		}

		parentBlockRecord, err = ecp.store.GetBlock(
			ctx,
			ecp.config.ChainId,
			parentBlockNum,
		)

		if err != nil || parentBlockRecord == nil {

			if errors.Is(err, persistence.ErrNotFound) {
				ecp.logger.Sugar().Debugw("Block not found in storage",
					"blockNumber", parentBlockNum,
					"error", err)
				parentBlockRecord = &chainPoller.BlockRecord{
					Number:     canonParentBlock.Number.Value(),
					Hash:       canonParentBlock.Hash.Value(),
					ParentHash: canonParentBlock.ParentHash.Value(),
					Timestamp:  canonParentBlock.Timestamp.Value(),
					ChainId:    canonParentBlock.ChainId,
				}
				// Save the block from canonical chain to storage since it was missing
				if saveErr := ecp.store.SaveBlock(ctx, parentBlockRecord); saveErr != nil {
					ecp.logger.Sugar().Warnw("Failed to save missing block to storage",
						"blockNumber", parentBlockNum,
						"error", saveErr)
				}
			} else {
				return nil, fmt.Errorf("failed to fetch block %d for : %w", parentBlockNum, err)
			}
		}

		if canonParentBlock.Hash.Value() != parentBlockRecord.Hash {
			ecp.logger.Sugar().Infow("Found orphaned block",
				"blockNumber", parentBlockNum,
				"storedBlockHash", parentBlockRecord.Hash,
				"canonChildBlockHash", canonParentBlock.Hash.Value(),
				"searchDepth", startBlockNumber-parentBlockNum)

			orphanedBlocks = append(orphanedBlocks, parentBlockRecord)
			continue
		}

		ecp.logger.Sugar().Infow("Block hash match, stopping reorg ancestry search",
			"blockNumber", parentBlockNum,
			"storedBlockHash", parentBlockRecord.Hash,
			"canonChildBlockHash", canonParentBlock.Hash.Value())

		return orphanedBlocks, ecp.store.SaveBlock(ctx, parentBlockRecord)
	}

	ecp.logger.Sugar().Warn("Reached max reorg search depth")

	return orphanedBlocks, ecp.store.SaveBlock(ctx, parentBlockRecord)
}
