package main

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/spf13/cobra"
	"github.com/tyler-smith/go-bip32"
	"github.com/tyler-smith/go-bip39"
	"log"
	"math/big"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

var chainID int64 = 202588

func transferCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "transfer",
		Short: "transfer ju",
		Run:   transferCoin,
	}
	addTransferFlags(cmd)
	return cmd
}

func addTransferFlags(cmd *cobra.Command) {
	cmd.Flags().IntP("repeat", "r", 1, "repeat number")
	_ = cmd.MarkFlagRequired("repeat")
	cmd.Flags().IntP("start_index", "i", 0, "start index")
	cmd.Flags().StringP("mnemonic", "m", "", "mnemonic")
	_ = cmd.MarkFlagRequired("mnemonic")
}

func transferCoin(cmd *cobra.Command, args []string) {
	rpcLaddr, _ := cmd.Flags().GetString("rpc_laddr")
	chainID, _ = cmd.Flags().GetInt64("signChainID")
	startIndex, _ := cmd.Flags().GetInt("start_index")
	repeat, _ := cmd.Flags().GetInt("repeat")
	mnemonic, _ := cmd.Flags().GetString("mnemonic")
	transfer(rpcLaddr, startIndex, repeat, mnemonic)
}

func transfer(rpcLaddr string, start, repeat int, mnemonic string) {
	fmt.Println("chainID:", chainID)
	seed, err := bip39.NewSeedWithErrorChecking(mnemonic, "")
	if err != nil {
		fmt.Println("NewSeedWithErrorChecking with error:", err.Error())
		return
	}
	masterKey, err := bip32.NewMasterKey(seed)
	masterpriv, err := crypto.ToECDSA(masterKey.Key)
	if nil != err {
		fmt.Println("NewSeedWithErrorChecking with error:", err.Error())
		return
	}

	ethSender := crypto.PubkeyToAddress(masterpriv.PublicKey)
	dpub, _ := crypto.DecompressPubkey(masterKey.PublicKey().Key)
	ethSender2 := crypto.PubkeyToAddress(*dpub)
	fmt.Println("master ethSender:", ethSender.String())
	fmt.Println("master ethSender:", ethSender2.String())
	masterPrivHex := hex.EncodeToString(masterKey.Key)
	fmt.Println("masterPriv:", masterPrivHex)

	//开始批量构造交易
	client, err := ethclient.Dial(rpcLaddr)
	if err != nil {
		fmt.Println("Failed to connect to the Ethereum client with err:", err)
		return
	}

	nonce, err := client.NonceAt(context.Background(), ethSender, nil)
	if err != nil {
		fmt.Println("client.NonceAt with err:", err)
		return
	}
	fmt.Println("current nonce:", nonce, "sender:", ethSender)
	mSender := new(multiSender)
	mSender.client = client
	mSender.sender = ethSender
	mSender.senderKey = masterpriv
	mSender.nonce_start = nonce
	mSender.recvTxChan = make(chan *types.Transaction, 20000)

	// build send coin targets' transactions and sign
	var privkeyArr []*ecdsa.PrivateKey
	var txs []*types.Transaction
	for i := 0; i < repeat; i++ {
		bkey, err := newKeyFromMasterKey(masterKey, TypeEther, bip32.FirstHardenedChild, 0, uint32(i+start))
		if err != nil {
			fmt.Println("Failed to newKeyFromMasterKey with err:", err)
			return
		}
		newKey, err := crypto.ToECDSA(bkey.Key)
		privkeyArr = append(privkeyArr, newKey)
		cpub, err := crypto.DecompressPubkey(bkey.PublicKey().Key)
		if err != nil {
			fmt.Println("Failed to DecompressPubkey with err:", err)
			return
		}
		addr := crypto.PubkeyToAddress(*cpub)
		tx := mSender.SignJuTx(addr, mSender.nonce_start+uint64(i), big.NewInt(1).Mul(big.NewInt(1e18), big.NewInt(10)))

		txs = append(txs, tx)
	}
	fmt.Println("+++++++total signed txnum:", len(txs))
	time.Sleep(time.Second * 2)
	// 并发处理交易发送
	numCPUs := runtime.NumCPU()
	mSender.proceeNum = numCPUs
	for i := 0; i < mSender.proceeNum; i++ {
		go func(index int) {
			mSender.sendJuTxV2(index)
		}(i)
	}
	go func() {
		for _, tx := range txs {
			mSender.recvTxChan <- tx
		}
	}()

	for {
		time.Sleep(time.Millisecond * 300)
		fmt.Println("[      send txNum     ]:", mSender.sendTxNum, "process num:", mSender.proceeNum)
		if int(mSender.sendTxNum) == len(txs) {
			close(mSender.recvTxChan)
			break
		}
	}

	for {
		fmt.Println("process num:", mSender.proceeNum)
		if mSender.proceeNum == 0 {
			break
		}
		time.Sleep(time.Second)
	}

	transferStatics := checkTxStatusOpt(txs, client)

	now := "./transfer-" + fmt.Sprintf("%d", time.Now().Unix())
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
	transferStatics2DB.SendCnt = len(txs)

	writeToFile(now, transferStatics2DB)
	//writeCoinStatics(now, transferStatics2DB)
}

func checkTxProcessed(txs2check []*types.Transaction, client *ethclient.Client) {
	cnt := 0
	for {
		for _, tx := range txs2check {
			receipt, err := client.TransactionReceipt(context.Background(), tx.Hash())
			if err == ethereum.NotFound {
				time.Sleep(time.Second)
				cnt = 0
				continue
			}
			cnt++
			if cnt > 10 && cnt%10 == 0 {
				fmt.Println("checkTxProcessed has processed txs", cnt, "block height", receipt.BlockNumber.Int64())
			}
			continue
		}
		if cnt == len(txs2check) {
			break
		}
		cnt = 0
	}

}

func checkTxStatusOpt(txs2check []*types.Transaction, client *ethclient.Client) TransferStatics {
	numCPUs := runtime.NumCPU()
	txCnt := len(txs2check)
	txsPerGoroutime := txCnt / numCPUs
	fmt.Println("runtime.NumCPU()", numCPUs, "txsPerGoroutime", txsPerGoroutime)

	var txsStatus []*SingleTxStatus
	for i := range txs2check {
		txsStatus = append(txsStatus, &SingleTxStatus{
			TxHash:         txs2check[i].Hash(),
			CheckedAlready: false,
		})
	}

	var wg sync.WaitGroup
	resChan := make(chan *TransferStatics, 100)

	fmt.Println("Going to check tx status")

	if txsPerGoroutime == 0 {
		wg.Add(1)
		go checkTxGroupStatusOpt(0, txsStatus, client, &wg, resChan)
	} else {
		for i := 0; i < numCPUs-1; i++ {
			txs2txs2check := txsStatus[i*txsPerGoroutime : (i+1)*txsPerGoroutime]
			wg.Add(1)
			go checkTxGroupStatusOpt(i, txs2txs2check, client, &wg, resChan)
		}

		txs2txs2check := txsStatus[(numCPUs-1)*txsPerGoroutime:]
		wg.Add(1)
		go checkTxGroupStatusOpt(numCPUs-1, txs2txs2check, client, &wg, resChan)
	}
	wg.Wait()

	var transferStatics TransferStatics
	transferStatics.TxResult = make(map[string]TxStatus)
	transferStatics.Blocks = make(map[int64]bool)
	fmt.Println("finish all the tx receipt check,and going to do sum statics")

	wg.Add(1)
	go func() {
		for {
			select {
			case onetransferStatics := <-resChan:
				transferStatics.FailedCnt += onetransferStatics.FailedCnt
				transferStatics.SuccessCnt += onetransferStatics.SuccessCnt
				for hash, s := range onetransferStatics.TxResult {
					transferStatics.TxResult[hash] = s
				}
				for block, b := range onetransferStatics.Blocks {
					transferStatics.Blocks[block] = b
				}
				fmt.Println("len(resChan)", len(resChan))
				if len(resChan) == 0 {
					defer wg.Done()
					return
				}
			}
		}
	}()
	wg.Wait()

	return transferStatics
}

type SingleTxStatus struct {
	TxHash         common.Hash
	CheckedAlready bool
	Status         int
}

func checkTxGroupStatusOpt(processIndex int, txs2check []*SingleTxStatus, client *ethclient.Client, wg *sync.WaitGroup, resChan chan *TransferStatics) {
	fmt.Println("checkTxGroupStatusOpt", "len(txs2check)", len(txs2check))
	sleepCnt := 0
	var transferStatics TransferStatics
	transferStatics.TxResult = make(map[string]TxStatus)
	transferStatics.Blocks = make(map[int64]bool)
	start := time.Now()
	for {
		for _, tx := range txs2check {
			if tx.CheckedAlready == true {
				continue
			}
			receipt, err := client.TransactionReceipt(context.Background(), tx.TxHash)
			if err == ethereum.NotFound {
				fmt.Println("Not find tx with hash", tx.TxHash.String(), "going to sleep 1 sec")
				time.Sleep(time.Second)
				sleepCnt++
				continue
			} else if err != nil {
				tx.CheckedAlready = true
				atomic.AddInt32(&transferStatics.FailedCnt, 1)
				continue
			}

			tx.CheckedAlready = true

			transferStatics.Blocks[receipt.BlockNumber.Int64()] = true

			if receipt.Status != types.ReceiptStatusSuccessful {
				transferStatics.TxResult[tx.TxHash.String()] = TxStatus{
					Block:  receipt.BlockNumber.Int64(),
					Status: int(types.ReceiptStatusFailed),
				}
				atomic.AddInt32(&transferStatics.FailedCnt, 1)
				fmt.Println("tx failed with statue", receipt.Status)
				continue
			}

			transferStatics.TxResult[tx.TxHash.String()] = TxStatus{
				Block:  receipt.BlockNumber.Int64(),
				Status: int(types.ReceiptStatusSuccessful),
			}
			atomic.AddInt32(&transferStatics.SuccessCnt, 1)

			fmt.Println("checkTxGroupStatusOpt processIndex", processIndex, transferStatics.SuccessCnt, transferStatics.FailedCnt, int32(len(txs2check)))
			if transferStatics.SuccessCnt+transferStatics.FailedCnt >= int32(len(txs2check)) {
				fmt.Println("checkTxGroupStatusOpt", transferStatics.SuccessCnt, transferStatics.FailedCnt, int32(len(txs2check)))
				resChan <- &transferStatics
				wg.Done()
				return
			}

			if time.Since(start).Seconds() > float64(10) {
				fmt.Println("time.Since(start).Seconds()", time.Since(start).Seconds(), "checkTxGroupStatusOpt", transferStatics.SuccessCnt, transferStatics.FailedCnt, int32(len(txs2check)))
				resChan <- &transferStatics
				wg.Done()
				return
			}
		}
	}
}

type TxStatus struct {
	Block     int64  `json:"block,omitempty"`
	Status    int    `json:"status,omitempty"`
	ErrorInfo string `json:"errorInfo,omitempty"`
}

type TransferStatics struct {
	Name       string
	TxResult   map[string]TxStatus
	Blocks     map[int64]bool
	SuccessCnt int32
	FailedCnt  int32
}

type TxStatusStatics struct {
	Name       string
	RWLock     sync.RWMutex
	Tx2check   map[common.Hash]bool
	Blocks     map[int64]bool
	SuccessCnt int32
	FailedCnt  int32
}

type TransferStatics2DB struct {
	Name       string              `json:"name,omitempty"`
	SuccessCnt int                 `json:"successCnt"`
	SendCnt    int                 `json:"sendCnt,omitempty"`
	FailedCnt  int                 `json:"failedCnt"`
	Blocks     map[int64]bool      `json:"blocks,omitempty"`
	TxResult   map[string]TxStatus `json:"txResult,omitempty"`
}

type multiSender struct {
	nonce_start uint64
	client      *ethclient.Client
	sender      common.Address
	senderKey   *ecdsa.PrivateKey
	recvTxChan  chan *types.Transaction
	proceeNum   int
	sendTxNum   int64
	mutex       sync.Mutex
	txMutex     sync.Mutex
	atoTxNum    int32
	repeat      int32
	cycle       int32
	gasPrice    *big.Int
}

var gasPrice = big.NewInt(0)

func (m *multiSender) sendJuTxV2(i int) {
	defer func() {
		m.mutex.Lock()
		m.proceeNum--
		m.mutex.Unlock()
	}()
	for {
		tx, ok := <-m.recvTxChan
		if ok {
			err := m.client.SendTransaction(context.Background(), tx)
			if err != nil {
				m.txMutex.Lock()
				m.sendTxNum++
				m.txMutex.Unlock()

				log.Println("Failed to send transaction:", err, "sendTx:", m.atoTxNum)
				break
			}
			m.txMutex.Lock()
			m.sendTxNum++
			m.txMutex.Unlock()
			atomic.AddInt32(&m.atoTxNum, 1)
			//fmt.Println("Transaction sent:", tx.Hash().Hex(), "process index:", i, "atoTxnum:", m.atoTxNum, "m.sendTxNum:", m.sendTxNum)
		} else {
			//println("channel closed")
			return
		}
	}
}

func (m *multiSender) SignJuTx(to common.Address, nonce uint64, amount *big.Int) *types.Transaction {

	gasLimit := uint64(21000) // 交易的Gas Limit
	var err error

	if gasPrice.Int64() == 0 {
		gasPrice, err = m.client.SuggestGasPrice(context.Background())
		if err != nil {
			log.Fatalf("Failed to suggest gas price: %v", err)
		}
	}

	// 5. 创建交易
	//
	fmt.Println("gasPrice", gasPrice, "nonce", nonce, "amount", amount)
	tx := types.NewTransaction(nonce, to, amount, gasLimit, big.NewInt(3e9), nil)
	// 6. 使用私钥签名交易

	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(big.NewInt(chainID)), m.senderKey)
	if err != nil {
		log.Fatalf("Failed to sign transaction: %v", err)
	}
	return signedTx
}

const Purpose uint32 = 0x8000002C
const TypeEther uint32 = 0x8000003c

func newKeyFromMasterKey(masterKey *bip32.Key, coin, account, chain, address uint32) (*bip32.Key, error) {
	child, err := masterKey.NewChildKey(Purpose)
	if err != nil {
		return nil, err
	}

	child, err = child.NewChildKey(coin)
	if err != nil {
		return nil, err
	}

	child, err = child.NewChildKey(account)
	if err != nil {
		return nil, err
	}

	child, err = child.NewChildKey(chain)
	if err != nil {
		return nil, err
	}

	child, err = child.NewChildKey(address)
	if err != nil {
		return nil, err
	}

	return child, nil
}
