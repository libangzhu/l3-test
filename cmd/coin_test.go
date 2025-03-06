package main

import (
	"encoding/hex"
	"fmt"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/tyler-smith/go-bip32"
	"github.com/tyler-smith/go-bip39"
	"testing"
)

//cupboard kiss effort champion faith bread firm cruise tissue decide all rotate
//0x2fb263f0cf418cf1f3fb4655f45b06915780d8f4
//0x8837f766507308bb0a936c1a911f00701b526519

func TestPrivateKey(t *testing.T) {

	mnemonic := "cupboard kiss effort champion faith bread firm cruise tissue decide all rotate"

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

	fmt.Println("masterPriv:", hex.EncodeToString(masterKey.Key))
}

func TestSendCoinPerf(t *testing.T) {
	mnemonic := "cupboard kiss effort champion faith bread firm cruise tissue decide all rotate"
	rpcLaddr := "http://52.74.204.233:8545"
	repeat := 3000
	transfer(rpcLaddr, repeat, mnemonic)
}
