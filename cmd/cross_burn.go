package main

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/log"
	"github.com/shopspring/decimal"
	"github.com/spf13/cobra"
	"github.com/tyler-smith/go-bip32"
	"github.com/tyler-smith/go-bip39"
	EvmSrc "github.com/zhengjunhe/l3-test/cmd/contracts/contracts4EvmSrc/generated"
	"github.com/zhengjunhe/l3-test/cmd/contracts/contracts4juchain/generated"
	erc20 "github.com/zhengjunhe/l3-test/cmd/contracts/erc20/generated"
	"github.com/zhengjunhe/l3-test/cmd/ethinterface"
	"math/big"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func crossBurnCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "burn",
		Short: "burn from ju",
		Run:   burnToken,
	}
	addBurnFlags(cmd)
	return cmd
}

func addBurnFlags(cmd *cobra.Command) {
	cmd.Flags().IntP("repeat", "r", 1, "repeat number")
	_ = cmd.MarkFlagRequired("repeat")
	cmd.Flags().IntP("chainID", "w", 97, "cross to dst chain ID")
	cmd.Flags().Float64P("amount", "a", 0, "amount of burn token")
	cmd.Flags().StringP("token", "t", "", "token contract address")
	cmd.Flags().StringP("registerAddr", "c", "", "registerAddr contract address")
	cmd.Flags().StringP("key", "k", "", "sender private key ")
	cmd.Flags().BoolP("coins", "j", false, "test transfer and cross burn at the same time")
	cmd.Flags().IntP("coinsRepeat", "s", 0, "transfer repeat number")
	_ = cmd.MarkFlagRequired("amount")
	_ = cmd.MarkFlagRequired("chainID")
	_ = cmd.MarkFlagRequired("registerAddr")
	_ = cmd.MarkFlagRequired("key")
	cmd.Flags().StringP("mnemonic", "m", "", "mnemonic")
	_ = cmd.MarkFlagRequired("mnemonic")

}

func burnToken(cmd *cobra.Command, args []string) {
	rpcLaddr, _ := cmd.Flags().GetString("rpc_laddr")
	repeat, _ := cmd.Flags().GetInt("repeat")
	amount, _ := cmd.Flags().GetFloat64("amount")
	token, _ := cmd.Flags().GetString("token")
	registerAddr, _ := cmd.Flags().GetString("registerAddr")
	coinsRepeat, _ := cmd.Flags().GetInt("coinsRepeat")
	chainID, err := cmd.Flags().GetInt("chainID")
	if err != nil {
		panic(err)
	}
	key, err := cmd.Flags().GetString("key")
	if err != nil {
		panic(err)
	}
	mnemonic, _ := cmd.Flags().GetString("mnemonic")
	sendCoins, _ := cmd.Flags().GetBool("coins")

	burn(mnemonic, key, registerAddr, token, rpcLaddr, repeat, coinsRepeat, int64(chainID), amount, sendCoins)
}

func burn(mnemonic, priv, registerAddr, token, rpcLaddr string, burnRepeat, coinsRepeat int, chainID int64, amount float64, sendCoins bool) {
	seed, err := bip39.NewSeedWithErrorChecking(mnemonic, "")
	if err != nil {
		fmt.Println("NewSeedWithErrorChecking with error:", err.Error())
		return
	}

	masterKey, err := bip32.NewMasterKey(seed)
	d, err := GetDecimalsFromNode(token, rpcLaddr)
	if err != nil {
		fmt.Println("get decimals err")
		return
	}

	client, x2EthContracts, x2EthDeployInfo, err := RecoverContractHandler4Dstju(registerAddr, rpcLaddr)
	if err != nil {
		fmt.Println("RecoverContractHandler err:", err)
		return
	}

	burnKey, err := crypto.ToECDSA(common.FromHex(priv))
	if err != nil {
		fmt.Println("priv:", priv)
		panic(err)
	}

	coinsKey, err := crypto.ToECDSA(masterKey.Key)
	if err != nil {
		panic(err)
	}
	var txHashMutex sync.Mutex
	var txHashes []string
	var mSender multiSender
	mSender.sender = crypto.PubkeyToAddress(coinsKey.PublicKey)
	mSender.nonce_start, _ = client.NonceAt(context.Background(), crypto.PubkeyToAddress(coinsKey.PublicKey), nil)
	mSender.senderKey = coinsKey
	mSender.client, err = ethclient.Dial(rpcLaddr)
	if err != nil {
		panic(err)
	}
	burnSenderNonce, _ := client.NonceAt(context.Background(), crypto.PubkeyToAddress(burnKey.PublicKey), nil)
	fmt.Println("burn  sender:", crypto.PubkeyToAddress(burnKey.PublicKey), "nonce:", burnSenderNonce)
	fmt.Println("coins  sender:", crypto.PubkeyToAddress(coinsKey.PublicKey), "nonce:", mSender.nonce_start)
	var wg sync.WaitGroup
	var repeat = burnRepeat
	if repeat < coinsRepeat {
		repeat = coinsRepeat
	}
	var bSender *burnSender
	if burnRepeat != 0 {
		bSender = &burnSender{
			client:         client,
			senderKey:      burnKey,
			sender:         crypto.PubkeyToAddress(burnKey.PublicKey),
			bridgeBankIns:  x2EthContracts.BridgeBank,
			bridgeBankAddr: x2EthDeployInfo.BridgeBank.Address,
			tokenAddr:      common.HexToAddress(token),
			amount:         ToWei(amount/float64(burnRepeat), d),
			chainID2wd:     big.NewInt(chainID),
		}
		addr2NonceOnly4test[bSender.sender] = &NonceMutex{int64(burnSenderNonce) - 1}
		_, err = bSender.Approve(ToWei(amount, d), int64(burnSenderNonce))
		if err != nil {
			fmt.Println("Approve failed:", err)
			return
		}
	}

	for i := 0; i < repeat; i++ {
		bkey, err := newKeyFromMasterKey(masterKey, TypeEther, bip32.FirstHardenedChild, 0, uint32(i))
		if err != nil {
			fmt.Println("Failed to newKeyFromMasterKey with err:", err)
			return
		}
		cPub, err := crypto.DecompressPubkey(bkey.PublicKey().Key)
		if err != nil {
			panic(err)
		}
		if i < burnRepeat {
			wg.Add(1)
			go func(toAddr common.Address, index int) {
				defer func() {
					wg.Done()
				}()
				fmt.Println("signBurnTx index", index)
				signedTx, err := bSender.SignBurn(toAddr)
				if err != nil {
					fmt.Println("Burn err", err)
					return
				}
				waitBurnChan <- signedTx
				txHashMutex.Lock()
				txHashes = append(txHashes, signedTx.burnHash.Hex())
				txHashMutex.Unlock()

			}(crypto.PubkeyToAddress(*cPub), i)

		}

		//coins transfer
		if sendCoins && i < coinsRepeat {
			addr := crypto.PubkeyToAddress(*cPub)
			nonce := mSender.nonce_start + uint64(i)
			wg.Add(1)
			go func(to common.Address, sNonce uint64, index int) {
				defer func() {
					wg.Done()
				}()
				fmt.Println("signJuTx index", index)
				tx := mSender.SignJuTx(to, sNonce, big.NewInt(1e10))
				signedTx := &waitBurn{
					to:       to.Hex(),
					amount:   tx.Value(),
					burnHash: tx.Hash(),
					client:   client,
					tx:       tx,
				}
				waitBurnChan <- signedTx
				txHashMutex.Lock()
				txHashes = append(txHashes, signedTx.burnHash.Hex())
				txHashMutex.Unlock()

			}(addr, nonce, i)

		}

	}
	wg.Wait()
	fmt.Println("signed tx completed txhashes:", len(txHashes), "will send thoes tx by 50 go process")
	time.Sleep(time.Second * 10)
	go sendBurnTx(50)

	for {
		fmt.Println("wait  burned tx send current send num:", len(waitCheckTxStatus), "failednum:", len(failedBurn), "sendCount:", sendTxNum)
		if len(waitCheckTxStatus)+len(failedBurn) == len(txHashes) {
			close(waitBurnChan)
			break
		}

		time.Sleep(time.Millisecond * 800)
	}

	transferStatics := checkTxStatusOpt(waitCheckTxStatus, client)
	now := "./burn-" + fmt.Sprintf("%d", time.Now().Unix())
	var transferStatics2DB TransferStatics2DB

	transferStatics2DB.Name = "transferStatics"
	transferStatics2DB.TxResult = make(map[string]TxStatus)
	transferStatics2DB.Blocks = make(map[int64]bool)
	for hash, val := range transferStatics.TxResult {
		transferStatics2DB.TxResult[hash] = val
	}

	for block, val := range transferStatics.Blocks {
		transferStatics2DB.Blocks[block] = val
	}

	transferStatics2DB.SuccessCnt = int(transferStatics.SuccessCnt)
	transferStatics2DB.FailedCnt = int(transferStatics.FailedCnt)
	transferStatics2DB.SendCnt = len(txHashes)

	writeToFile(now, transferStatics2DB)

}

func sendBurnTx(proceeNum int) {
	//多协程批量处理
	for i := 0; i < proceeNum; i++ {
		go func() {
			for {
				waitBurn, ok := <-waitBurnChan
				if ok {
					err := waitBurn.client.SendTransaction(context.Background(), waitBurn.tx)
					if nil != err {
						fmt.Println(" SendTransaction err", err.Error())
						waitCheckMutex.Lock()
						failedBurn = append(failedBurn, waitBurn.tx)
						waitCheckMutex.Unlock()
						continue
					}

					waitCheckMutex.Lock()
					waitCheckTxStatus = append(waitCheckTxStatus, waitBurn.tx)
					waitCheckMutex.Unlock()
					atomic.AddInt32(&sendTxNum, 1)

				} else {
					fmt.Println("channel closed")
					return
				}
			}
		}()
	}

}

// -----------------------------

var (
	waitCheckMutex    sync.Mutex
	waitCheckTxStatus []*types.Transaction
	failedBurn        []*types.Transaction
	sendTxNum         int32
	waitBurnChan      = make(chan *waitBurn, 102400)
)

type childKeyAddr struct {
	key   *ecdsa.PrivateKey
	addr  common.Address
	index int
	nonce int64
}

type burnSender struct {
	sender           common.Address
	senderKey        *ecdsa.PrivateKey
	client           ethinterface.EthClientSpec
	tokenAddr        common.Address
	bridgeBankIns    *generated.BridgeBank
	bridgeBankAddr   common.Address
	chainID2wd       *big.Int
	amount           *big.Int
	repeat           int
	cycle            int
	proceeNum        int
	sendTxNum        int64
	sendErrTxNum     int64
	recvTxChan       chan *types.Transaction
	bridgeServiceFee *big.Int
	gasPrice         *big.Int
	nodeUrl          string
	//child            chan *childKeyAddr
	child   []*childKeyAddr
	approve bool
	view    bool
}

type waitBurn struct {
	to           string
	amount       *big.Int
	burnHash     common.Hash
	client       ethinterface.EthClientSpec
	providerHttp string
	isPeinding   bool
	tx           *types.Transaction
}

// X2EthContractsOnJu ...
type X2EthContractsOnJu struct {
	BridgeRegistry *generated.BridgeRegistry
	BridgeBank     *generated.BridgeBank
	BtcBridge      *generated.Bridge
	Valset         *generated.Valset
	Oracle         *generated.Oracle
}

type X2EthContractsOnEvmSrc struct {
	BridgeRegistry *EvmSrc.BridgeRegistry
	BridgeBank     *EvmSrc.BridgeBank
	BtcBridge      *EvmSrc.Bridge
	Valset         *EvmSrc.Valset
	Oracle         *EvmSrc.Oracle
}

// X2EthDeployResult ...
type X2EthDeployInfo struct {
	BridgeRegistry *DeployResult
	BridgeBank     *DeployResult
	BtcBridge      *DeployResult
	Valset         *DeployResult
	Oracle         *DeployResult
}

// DeployResult ...
type DeployResult struct {
	Address common.Address
	TxHash  string
}

// ContractRegistry :
type ContractRegistry byte

var addr2NonceOnly4test = make(map[common.Address]*NonceMutex)

const (
	// Valset : valset contract
	Valset ContractRegistry = iota + 1
	// Oracle : oracle contract
	Oracle
	// BridgeBank : bridgeBank contract
	BridgeBank
	// Bridge : btcBridge contract
	Bridge
)
const (
	GasLimit                    = uint64(80 * 10000)
	GasLimit4RelayTx            = uint64(80 * 10000) //这个地方可能会由于链的不同或者升级，导致gas变多，超出limit，定位方法是查看Internal Txns 的 trace 里是不是有gas out报错
	GasLimit4Deploy             = uint64(0)
	PendingDuration4TxExeuction = 300
)

// Approve
func (b *burnSender) Approve(amount *big.Int, nonce int64) (string, error) {

	var prepareDone bool
	var err error
	defer func() {
		if err != nil && prepareDone {
			_, _ = revokeNonce(b.sender)
		}
	}()

	auth, err := PrepareAuth(b.client, b.senderKey, b.sender)
	if nil != err {

		return "", err
	}
	auth.Nonce = big.NewInt(nonce)

	prepareDone = true

	//tokenAddr := common.HexToAddress(tokenAddrstr)
	tokenInstance, err := generated.NewBridgeToken(b.tokenAddr, b.client)
	if nil != err {

		return "", err
	}

	//btcbank 是bridgeBank的基类，所以使用bridgeBank的地址
	tx, err := tokenInstance.Approve(auth, b.bridgeBankAddr, amount)
	if nil != err {

		return "", err
	}

	err = waitEthTxFinished(b.client, tx.Hash(), "Approve")
	if nil != err {

		return "", err
	}

	return "", nil

}

// Burn ...
func (b *burnSender) SignBurn(receiver common.Address) (*waitBurn, error) {
	var err error
	var prepareDone bool
	defer func() {
		if err != nil && prepareDone {
			_, _ = revokeNonce(b.sender)
		}
	}()

	opts := &bind.CallOpts{
		Pending: false,
		From:    common.Address{},
		Context: context.Background(),
	}

	// add the servicefee to value
	if b.bridgeServiceFee == nil {
		bridgeServiceFee, err := b.bridgeBankIns.BridgeServiceFee(opts)
		if nil != err {
			log.Error("LockEthErc20Asset", "Get BridgeServiceFee err", err.Error())
			return nil, err
		}
		b.bridgeServiceFee = bridgeServiceFee
	}

	prepareDone = false
	auth, err := PrepareAuth(b.client, b.senderKey, b.sender)
	if nil != err {
		log.Error("Burn::PrepareAuth", "err", err.Error())
		return nil, err
	}
	prepareDone = true
	auth.Value.SetInt64(b.bridgeServiceFee.Int64())
	auth.NoSend = true

	//fmt.Println("chainIDWd:", b.chainID2wd, "receiver:", receiver.Hex(), "tokenAddr:", b.tokenAddr, "amount:", b.amount)
	tx, err := b.bridgeBankIns.BurnBridgeTokens(auth, b.chainID2wd, receiver, b.tokenAddr, b.amount)
	if nil != err {
		log.Error("Burn::BurnBridgeTokens", "err", err.Error())
		return nil, err
	}

	return &waitBurn{
		to:       receiver.Hex(),
		amount:   b.amount,
		burnHash: tx.Hash(),
		client:   b.client,
		tx:       tx,
	}, nil

}

// GetDecimalsFromNode ...
func GetDecimalsFromNode(addr, rpcLaddr string) (int64, error) {
	if addr == "0x0000000000000000000000000000000000000000" || addr == "" {
		return 18, nil
	}

	client, err := ethclient.Dial(rpcLaddr)
	if err != nil {
		log.Error("GetDecimals", "SetupEthClient error:", err.Error())
		return 0, err
	}
	owner := common.HexToAddress(addr)
	caller, err := erc20.NewERC20Caller(owner, client)
	if err != nil {
		log.Error("GetDecimals", "NewERC20Caller error:", err.Error())
		return 0, err
	}
	opts := &bind.CallOpts{
		Pending: true,
		From:    owner,
		Context: context.Background(),
	}
	caller.Decimals(opts)
	decimals, err := caller.Decimals(opts)
	if err != nil {
		log.Error("GetDecimals", "ParseInt error:", err.Error())
		return 0, err
	}
	return int64(decimals), nil
}

func RecoverContractHandler4Dstju(registerAddr, nodeAddr string) (*ethclient.Client, *X2EthContractsOnJu, *X2EthDeployInfo, error) {
	if registerAddr == "" {
		return nil, nil, nil, fmt.Errorf("registerAddr is nil")
	}
	client, err := ethclient.Dial(nodeAddr)
	if err != nil {
		return nil, nil, nil, err
	}
	addr := common.HexToAddress(registerAddr)
	x2EthContracts, x2EthDeployInfo, err := RecoverContractHandler4ju(client, addr, addr)
	if err != nil {
		return nil, nil, nil, err
	}
	return client, x2EthContracts, x2EthDeployInfo, nil
}

func RecoverContractHandler4ju(client ethinterface.EthClientSpec, sender, registry common.Address) (*X2EthContractsOnJu, *X2EthDeployInfo, error) {
	bridgeBankAddr, err := GetAddressFromBridgeRegistry(client, sender, registry, BridgeBank)
	if nil != err {
		return nil, nil, errors.New("failed to get addr for bridgeBank from registry")
	}
	bridgeBank, err := generated.NewBridgeBank(*bridgeBankAddr, client)
	if nil != err {
		return nil, nil, errors.New("failed to NewBridgeBank")
	}

	btcBridgeAddr, err := GetAddressFromBridgeRegistry(client, sender, registry, Bridge)
	if nil != err {
		return nil, nil, errors.New("failed to get addr for btcBridgeAddr from registry")
	}
	btcBridge, err := generated.NewBridge(*btcBridgeAddr, client)
	if nil != err {
		return nil, nil, errors.New("failed to NewBtcBridge")
	}

	oracleAddr, err := GetAddressFromBridgeRegistry(client, sender, registry, Oracle)
	if nil != err {
		return nil, nil, errors.New("failed to get addr for oracleBridgeAddr from registry")
	}
	oracle, err := generated.NewOracle(*oracleAddr, client)
	if nil != err {
		return nil, nil, errors.New("failed to NewOracle")
	}

	valsetAddr, err := GetAddressFromBridgeRegistry(client, sender, registry, Valset)
	if nil != err {
		return nil, nil, errors.New("failed to get addr for valset from registry")
	}
	valset, err := generated.NewValset(*valsetAddr, client)
	if nil != err {
		return nil, nil, errors.New("failed to NewValset")
	}

	registryInstance, _ := generated.NewBridgeRegistry(registry, client)
	x2EthContracts := &X2EthContractsOnJu{
		BridgeRegistry: registryInstance,
		BridgeBank:     bridgeBank,
		BtcBridge:      btcBridge,
		Oracle:         oracle,
		Valset:         valset,
	}

	x2EthDeployInfo := &X2EthDeployInfo{
		BridgeRegistry: &DeployResult{Address: registry},
		BridgeBank:     &DeployResult{Address: *bridgeBankAddr},
		BtcBridge:      &DeployResult{Address: *btcBridgeAddr},
		Oracle:         &DeployResult{Address: *oracleAddr},
		Valset:         &DeployResult{Address: *valsetAddr},
	}

	return x2EthContracts, x2EthDeployInfo, nil
}

// GetAddressFromBridgeRegistry : utility method which queries the requested contract address from the BridgeRegistry
func GetAddressFromBridgeRegistry(client ethinterface.EthClientSpec, sender, registry common.Address, target ContractRegistry) (address *common.Address, err error) {
	sim, isSim := client.(*ethinterface.SimExtend)
	var header *types.Header

	if isSim {
		header, err = sim.Client.HeaderByNumber(context.Background(), nil)
		if err != nil {
			log.Error("Use the simulato GetAddressFromBridgeRegistry", "Failed to get HeaderByNumber due to:", err.Error())
			return nil, err
		}

	} else {
		header, err = client.HeaderByNumber(context.Background(), nil)
		if err != nil {
			log.Error("GetAddressFromBridgeRegistry", "Failed to get HeaderByNumber due to:", err.Error())
			return nil, err
		}
	}

	// Set up CallOpts auth
	auth := bind.CallOpts{
		Pending:     true,
		From:        sender,
		BlockNumber: header.Number,
		Context:     context.Background(),
	}

	// Initialize BridgeRegistry instance
	registryInstance, err := generated.NewBridgeRegistry(registry, client)
	if err != nil {
		log.Error("GetAddressFromBridgeRegistry", "Failed to NewBridgeRegistry to:", err.Error())
		return nil, err
	}

	switch target {
	case Valset:
		valsetAddress, err := registryInstance.Valset(&auth)
		if err != nil {
			log.Error("GetAddressFromBridgeRegistry", "Failed to get Valset contract:", err)
			return nil, err
		}
		return &valsetAddress, nil
	case Oracle:
		oracleAddress, err := registryInstance.Oracle(&auth)
		if err != nil {
			log.Error("GetAddressFromBridgeRegistry", "Failed to get oracle contract:", err)
			return nil, err
		}
		return &oracleAddress, nil
	case BridgeBank:
		bridgeBankAddress, err := registryInstance.BridgeBank(&auth)
		if err != nil {
			log.Error("GetAddressFromBridgeRegistry", "Failed to get BridgeBank contract:", err)
			return nil, err
		}
		return &bridgeBankAddress, nil
	case Bridge:
		btcBridgeAddress, err := registryInstance.BtcBridge(&auth)
		if err != nil {
			log.Error("GetAddressFromBridgeRegistry", "Failed to get BtcBridge contract:", err)
			return nil, err
		}
		return &btcBridgeAddress, nil
	default:
		log.Error("GetAddressFromBridgeRegistry", "invalid target contract type:", target)
		return nil, errors.New("ErrInvalidEthContractAddress")
	}
}

func revokeNonce4MultiEth(sender common.Address, addr2TxNonce map[common.Address]*NonceMutex) (*big.Int, error) {
	if nonceMutex, exist := addr2TxNonce[sender]; exist {
		//nonceMutex.RWLock.Lock()
		//defer nonceMutex.RWLock.Unlock()
		atomic.AddInt64(&nonceMutex.Nonce, -1)
		addr2TxNonce[sender] = nonceMutex
		log.Debug("revokeNonce4MultiEth", "address", sender.String(), "nonce", nonceMutex.Nonce)
		return big.NewInt(nonceMutex.Nonce), nil
	}
	return nil, errors.New("address doesn't exist tx")
}

func revokeNonce(sender common.Address) (*big.Int, error) {
	return revokeNonce4MultiEth(sender, addr2NonceOnly4test)
}

// ToWei 将eth单位的金额转为wei单位
func ToWei(amount float64, decimals int64) *big.Int {
	return decimal.NewFromFloat(amount).Mul(decimal.NewFromInt(Decimal2value[int(decimals)])).BigInt()

}

type NonceMutex struct {
	Nonce int64
}

var nMutex sync.Mutex

func PrepareAuth(client ethinterface.EthClientSpec, privateKey *ecdsa.PrivateKey, transactor common.Address) (*bind.TransactOpts, error) {
	return PrepareAuth4MultiEthereum(client, privateKey, transactor, addr2NonceOnly4test)
}

func PrepareAuth4MultiEthereum(client ethinterface.EthClientSpec, privateKey *ecdsa.PrivateKey, transactor common.Address, addr2TxNonce map[common.Address]*NonceMutex) (*bind.TransactOpts, error) {
	if nil == privateKey || nil == client {
		log.Error("PrepareAuth", "nil input parameter", "client", client, "privateKey", privateKey)
		return nil, errors.New("nil input parameter")
	}

	ctx := context.Background()

	var err error
	if gasPrice.Int64() == 0 {
		gasPrice, err = client.SuggestGasPrice(ctx)
		if err != nil {
			log.Error("PrepareAuth", "Failed to SuggestGasPrice due to:", err.Error())
			return nil, errors.New("failed to get suggest gas price " + err.Error())
		}
	}

	auth, err := bind.NewKeyedTransactorWithChainID(privateKey, big.NewInt(chainID))
	if err != nil {
		log.Error("PrepareAuth NewKeyedTransactorWithChainID", "err", err, "chainID", chainID)
		return nil, err
	}
	auth.Value = big.NewInt(0) // in wei
	auth.GasLimit = GasLimit
	auth.GasPrice = big.NewInt(gasPrice.Int64() * 2)
	nMutex.Lock()
	defer nMutex.Unlock()

	if auth.Nonce, err = getNonce4MultiEth(transactor, client, addr2TxNonce, false); err != nil {
		return nil, err
	}

	return auth, nil
}

// fromChain 是否从链上获取
func getNonce4MultiEth(sender common.Address, client ethinterface.EthClientSpec, addr2TxNonce map[common.Address]*NonceMutex, fromChain bool) (*big.Int, error) {
	if fromChain {
		return getNonceFromChain(sender, client, addr2TxNonce)
	}
	if nonceMutex, exist := addr2TxNonce[sender]; exist {
		atomic.AddInt64(&nonceMutex.Nonce, 1)
		addr2TxNonce[sender] = nonceMutex
		return big.NewInt(nonceMutex.Nonce), nil
	}
	return getNonceFromChain(sender, client, addr2TxNonce)
}

func getNonceFromChain(sender common.Address, client ethinterface.EthClientSpec, addr2TxNonce map[common.Address]*NonceMutex) (*big.Int, error) {

	nonce, err := client.PendingNonceAt(context.Background(), sender)
	if nil != err {
		return nil, err
	}
	//fmt.Println("getNonceFromChain address", sender.String(), "nonce", nonce)
	n := new(NonceMutex)
	n.Nonce = int64(nonce)
	//n.RWLock = new(sync.RWMutex)
	addr2TxNonce[sender] = n
	return big.NewInt(int64(nonce)), nil
}

func waitEthTxFinished(client ethinterface.EthClientSpec, txhash common.Hash, txName string) error {

	log.Info(txName, "Wait for tx to be finished executing with hash", txhash.String())
	timeout := time.NewTimer(PendingDuration4TxExeuction * time.Second)
	oneSecondtimeout := time.NewTicker(5 * time.Second)

	for {
		select {
		case <-timeout.C:
			log.Info(txName, "tx", "eth tx timeout")
			return errors.New("eth tx timeout")
		case <-oneSecondtimeout.C:
			_, err := client.TransactionReceipt(context.Background(), txhash)
			if err == ethereum.NotFound {
				continue
			} else if err != nil {

				return err
			}

			return nil
		}
	}
}

// IsWebsocketURL : returns true if the given URL is a websocket URL
func isWebsocketURL(rawurl string) bool {
	u, err := url.Parse(rawurl)
	if err != nil {
		log.Error("Error while parsing URL: %v", err)
		return false
	}
	return u.Scheme == "ws" || u.Scheme == "wss"
}

// SetupWebsocketEthClient : returns boolean indicating if a URL is valid websocket ethclient
func SetupWebsocketEthClient(ethURL string) (*ethclient.Client, error) {
	if strings.TrimSpace(ethURL) == "" {
		return nil, nil
	}

	if !isWebsocketURL(ethURL) {
		return nil, fmt.Errorf("invalid websocket eth client URL: %v", ethURL)
	}

	client, err := ethclient.Dial(ethURL)
	if err != nil {
		return nil, fmt.Errorf("error dialing websocket client %w", err)
	}

	return client, nil
}

var Decimal2value = map[int]int64{
	0:  1,
	1:  1e1,
	2:  1e2,
	3:  1e3,
	4:  1e4,
	5:  1e5,
	6:  1e6,
	7:  1e7,
	8:  1e8,
	9:  1e9,
	10: 1e10,
	11: 1e11,
	12: 1e12,
	13: 1e13,
	14: 1e14,
	15: 1e15,
	16: 1e16,
	17: 1e17,
	18: 1e18,
}
