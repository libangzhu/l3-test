package main

import (
	"context"
	"fmt"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/spf13/cobra"
	"github.com/tyler-smith/go-bip32"
	"github.com/tyler-smith/go-bip39"
	"math/big"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

func crossBurnStressCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "burnStress",
		Short: "burn from ju all of the time",
		Run:   burnTokenAT,
	}
	addBurnStressFlags(cmd)
	return cmd
}

func addBurnStressFlags(cmd *cobra.Command) {
	cmd.Flags().IntP("repeat", "r", 1, "repeat number")
	_ = cmd.MarkFlagRequired("repeat")
	cmd.Flags().IntP("chainID", "w", 97, "cross to dst chain ID")
	cmd.Flags().Float64P("amount", "a", 0, "amount of burn token")
	cmd.Flags().StringP("token", "t", "", "token contract address")
	cmd.Flags().StringP("registerAddr", "c", "", "registerAddr contract address")
	cmd.Flags().StringP("key", "k", "", "sender private key ")

	_ = cmd.MarkFlagRequired("amount")
	_ = cmd.MarkFlagRequired("chainID")
	_ = cmd.MarkFlagRequired("registerAddr")
	_ = cmd.MarkFlagRequired("key")
	cmd.Flags().StringP("mnemonic", "m", "", "mnemonic")
	_ = cmd.MarkFlagRequired("mnemonic")

}

func burnTokenAT(cmd *cobra.Command, args []string) {
	rpcLaddr, _ := cmd.Flags().GetString("rpc_laddr")
	amount, _ := cmd.Flags().GetFloat64("amount")
	token, _ := cmd.Flags().GetString("token")
	registerAddr, _ := cmd.Flags().GetString("registerAddr")
	repeat, _ := cmd.Flags().GetInt("repeat")
	chainIDWd, err := cmd.Flags().GetInt("chainID")
	key, err := cmd.Flags().GetString("key")
	chainID, _ = cmd.Flags().GetInt64("signChainID")
	if err != nil {
		panic(err)
	}
	mnemonic, _ := cmd.Flags().GetString("mnemonic")
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

	burnKey, err := crypto.ToECDSA(common.FromHex(key))
	if err != nil {
		panic(err)
	}
	burnSenderNonce, _ := client.NonceAt(context.Background(), crypto.PubkeyToAddress(burnKey.PublicKey), nil)

	var bSender *burnSender
	bSender = &burnSender{
		client:         client,
		senderKey:      burnKey,
		sender:         crypto.PubkeyToAddress(burnKey.PublicKey),
		bridgeBankIns:  x2EthContracts.BridgeBank,
		bridgeBankAddr: x2EthDeployInfo.BridgeBank.Address,
		tokenAddr:      common.HexToAddress(token),
		amount:         big.NewInt(1),
		chainID2wd:     big.NewInt(int64(chainIDWd)),
		repeat:         repeat,
		recvTxChan:     make(chan *types.Transaction, 20000),
		proceeNum:      50,
		nodeUrl:        rpcLaddr,
	}
	addr2NonceOnly4test[bSender.sender] = &NonceMutex{int64(burnSenderNonce) - 1}
	_, err = bSender.Approve(ToWei(amount, d), int64(burnSenderNonce))
	if err != nil {
		fmt.Println("Approve failed:", err)
		if err.Error() == "already known" {

		} else {
			panic(err)
		}
	}

	fmt.Println("Approve success")

	signChan := make(chan *types.Transaction, 10000)
	go signBurnTxAT(masterKey, bSender, signChan)
	go waitSignBurnTxAT(signChan, bSender)
	go waitSendBurnTxAT(bSender)
	processStart := time.Now()

	for {
		fmt.Println("signChan capacity", len(signChan), "recvTxChan capacity", len(bSender.recvTxChan), "total send tx:", bSender.sendTxNum, "cycle times:", bSender.cycle, "cost time:", time.Since(processStart))
		time.Sleep(time.Second)
	}
}

func signBurnTxAT(masterKey *bip32.Key, sender *burnSender, sigChan chan *types.Transaction) {

	var count int64
	numCPUs := runtime.NumCPU()
	for i := 0; i < numCPUs; i++ {
		go func(cpuCore int) {
			for {

				bkey, err := newKeyFromMasterKey(masterKey, TypeEther, bip32.FirstHardenedChild, 0, uint32(atomic.AddInt64(&count, 1)))
				if err != nil {
					fmt.Println("Failed to newKeyFromMasterKey with err:", err)
					panic(err)
				}

				cpub, err := crypto.DecompressPubkey(bkey.PublicKey().Key)
				if err != nil {
					fmt.Println("Failed to DecompressPubkey with err:", err)
					panic(err)
				}

				addr := crypto.PubkeyToAddress(*cpub)
				signedTx, err := sender.SignBurn(addr)
				if err != nil {
					fmt.Println("Burn err", err)
					return
				}

				sigChan <- signedTx.tx
			}
		}(i)
	}

}

func waitSignBurnTxAT(sigChan chan *types.Transaction, sender *burnSender) {
	for {
		if len(sigChan) >= sender.repeat {
			for tx := range sigChan {
				sender.recvTxChan <- tx
				if len(sender.recvTxChan) == sender.repeat {
					//send signal for mutisend
					//每间隔3000笔交易就发送信号批量集中发送达到压测的目的
					for i := 0; i < sender.proceeNum; i++ {
						runChan <- true

					}
					break
				}
			}
		}

		time.Sleep(1 * time.Second)
	}
}

var nonceMutex sync.Mutex

func waitSendBurnTxAT(sender *burnSender) {

	if sender.proceeNum == 0 {
		panic("must set proceeNum")
	}
	for i := 0; i < sender.proceeNum; i++ {
		go func(index int) {
			for {
				client, err := ethclient.Dial(sender.nodeUrl)
				if err != nil {
					fmt.Println(err)
					panic(err)
				}
				<-runChan
				for tx := range sender.recvTxChan {

					err := client.SendTransaction(context.Background(), tx)
					if err != nil {
						fmt.Println("Failed to sendTxAT with err:", err, "will retry...")

						signer := types.NewEIP155Signer(tx.ChainId())
						from, _ := types.Sender(signer, tx)
						nonceMutex.Lock()
						_, _ = revokeNonce(from)
						nonceMutex.Unlock()
						continue
					}
					atomic.AddInt64(&sender.sendTxNum, 1)
					if sender.view {
						fmt.Println("send tx at:", tx.Hash().Hex(), "processNum:", index, "tx.Nonce:", tx.Nonce())
					}

					if len(sender.recvTxChan) == 0 {
						//fmt.Println("break,processNum:", index)
						break
					}
				}

			}
		}(i)
	}
}
