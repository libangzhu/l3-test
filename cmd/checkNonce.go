package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/spf13/cobra"
	"github.com/tyler-smith/go-bip32"
	"github.com/tyler-smith/go-bip39"
	"math/big"
)

func checkNonceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "checkNonce",
		Short: "check address nonce",
		Run:   checkNonce,
	}
	addCheckNonce(cmd)
	return cmd
}

func addCheckNonce(cmd *cobra.Command) {

	cmd.Flags().IntP("addrNum", "a", 1, "address number")
	_ = cmd.MarkFlagRequired("addrNum")

	cmd.Flags().StringP("mnemonic", "m", "", "mnemonic")
	_ = cmd.MarkFlagRequired("mnemonic")

}

func checkNonce(cmd *cobra.Command, args []string) {
	rpcLaddr, _ := cmd.Flags().GetString("rpc_laddr")
	addrNum, _ := cmd.Flags().GetInt("addrNum")
	mnemonic, _ := cmd.Flags().GetString("mnemonic")
	transferForCheckNonce(rpcLaddr, addrNum, mnemonic)
}

func transferForCheckNonce(rpcLaddr string, addrNum int, mnemonic string) {
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
	fmt.Println("master ethSender:", ethSender.String())
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
	var bSender *burnSender
	bSender = &burnSender{
		client:   client,
		repeat:   addrNum,
		keyStore: make(map[common.Address]*sigleSigner),
	}

	// 提前生成多个地址(repeat)信息
	bSender.genMutiKeyAddr(masterKey)
	sendCoins(bSender)

}

func sendCoins(bSender *burnSender) {
	gasLimit := uint64(21000) // 交易的Gas Limit
	var err error
	eClient, _ := bSender.client.(*ethclient.Client)

	gasPrice, err = bSender.client.SuggestGasPrice(context.Background())
	if err != nil {
		panic(err)
	}

	for addr, key := range bSender.keyStore {
		if addr.Cmp(common.HexToAddress("0x3b6a84292F6Da6fc0F8fDD7ce8E17455B7a7A583")) == 0 {
			fmt.Println("addr:", addr.Hex(), "privkey:", common.Bytes2Hex(crypto.FromECDSA(key.key)), "gasprice:", gasPrice)
			nonce, _ := eClient.NonceAt(context.Background(), addr, nil)
			tx := types.NewTransaction(nonce, bSender.sender, big.NewInt(1e14), gasLimit, big.NewInt(gasPrice.Int64()*3), nil)
			// 6. 使用私钥签名交易

			signedTx, err := types.SignTx(tx, types.NewEIP155Signer(big.NewInt(chainID)), key.key)
			if err != nil {
				panic(err)
			}
			err = eClient.SendTransaction(context.Background(), signedTx)
			if err != nil {
				fmt.Println("err:", err.Error(), "txgasprice:", tx.GasPrice(), "nonce:", tx.Nonce())
			}
		}

	}

}
