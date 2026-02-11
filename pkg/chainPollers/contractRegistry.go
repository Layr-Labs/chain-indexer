package chainPoller

type IContractRegistry interface {
	RegisterContracts(addresses []string)
	UnregisterContracts(addresses []string)
	ListContracts() []string
}
