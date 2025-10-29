package chainPoller

import (
	"context"

	"github.com/Layr-Labs/chain-indexer/pkg/clients/ethereum"
	"github.com/Layr-Labs/chain-indexer/pkg/config"
	"github.com/Layr-Labs/chain-indexer/pkg/transactionLogParser/log"
)

type IChainPoller interface {
	Start(ctx context.Context) error
}

type LogWithBlock struct {
	Log    *log.DecodedLog
	RawLog *ethereum.EthereumEventLog
	Block  *ethereum.EthereumBlock
}

type BlockRecord struct {
	Number     uint64
	Hash       string
	ParentHash string
	Timestamp  uint64
	ChainId    config.ChainId
}

type IChainPollerPersistence interface {
	GetLastProcessedBlock(ctx context.Context, avsAddress string, chainId config.ChainId) (*BlockRecord, error)

	SaveBlock(ctx context.Context, avsAddress string, block *BlockRecord) error
	GetBlock(ctx context.Context, avsAddress string, chainId config.ChainId, blockNumber uint64) (*BlockRecord, error)
	DeleteBlock(ctx context.Context, avsAddress string, chainId config.ChainId, blockNumber uint64) error
}
