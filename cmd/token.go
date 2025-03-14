package main

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/spf13/cobra"
	"github.com/tyler-smith/go-bip32"
	"github.com/tyler-smith/go-bip39"
	erc20 "github.com/zhengjunhe/l3-test/cmd/contracts/erc20/generated"
	"log"
	"math/big"
	"strings"
	"time"
)

// tokenCmd USDT transfer
func tokenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "transfer ju-usdt to other address",
		Run:   tokenTransfer,
	}
	addTokenTransferFlags(cmd)
	return cmd
}

func addTokenTransferFlags(cmd *cobra.Command) {
	cmd.Flags().IntP("repeat", "r", 1, "repeat number")
	_ = cmd.MarkFlagRequired("repeat")
	cmd.Flags().StringP("token", "t", "0x35904d7288e3Cda382090603C8D00D089A89a28C", "token contract address default usdt contract address")

	cmd.Flags().StringP("mnemonic", "m", "", "mnemonic")
	_ = cmd.MarkFlagRequired("mnemonic")
}

func tokenTransfer(cmd *cobra.Command, args []string) {
	rpcLaddr, _ := cmd.Flags().GetString("rpc_laddr")
	repeat, _ := cmd.Flags().GetInt("repeat")
	mnemonic, _ := cmd.Flags().GetString("mnemonic")
	tokenAddr, _ := cmd.Flags().GetString("token")
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
	time.Sleep(time.Second * 5)
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
	mSender.gasPrice, err = client.SuggestGasPrice(context.Background())
	if err != nil {
		panic(err)
	}
	var privkeyArr []*ecdsa.PrivateKey
	var txs []*types.Transaction
	for i := 0; i < repeat; i++ {
		bkey, err := newKeyFromMasterKey(masterKey, TypeEther, bip32.FirstHardenedChild, 0, uint32(i))
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
		tx := mSender.SignJuTokenTx(common.HexToAddress(tokenAddr), addr, mSender.nonce_start+uint64(i), big.NewInt(1e12))

		txs = append(txs, tx)
	}

	fmt.Println("+++++++total signed txnum:", len(txs))
	time.Sleep(time.Second * 2)
	mSender.proceeNum = 50
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

	now := "./transfer-token" + fmt.Sprintf("%d", time.Now().Unix())
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

}

func (m *multiSender) SignJuTokenTx(tokenAddr, to common.Address, nonce uint64, amount *big.Int) *types.Transaction {

	parsedABI, err := abi.JSON(strings.NewReader(erc20.ERC20ABI))
	if err != nil {
		panic(err)
	}

	// 构建转账数据
	data, err := parsedABI.Pack("transfer", to, amount)
	if err != nil {
		panic(err)
	}
	// gasPrice = gasPrice.Mul(gasPrice, big.NewInt(11)).Div(gasPrice, big.NewInt(10))
	// 5. 创建交易
	tx := types.NewTransaction(nonce, tokenAddr, big.NewInt(0), GasLimit, m.gasPrice, data)
	// 6. 使用私钥签名交易

	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(big.NewInt(chainID)), m.senderKey)
	if err != nil {
		log.Fatalf("Failed to sign transaction: %v", err)
	}
	fmt.Println("sign tx nonce:", nonce, "tx.to:", to)
	return signedTx
}
