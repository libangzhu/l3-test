package main

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"fmt"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/spf13/cobra"
	"github.com/tyler-smith/go-bip32"
	"github.com/tyler-smith/go-bip39"
	"github.com/zhengjunhe/l3-test/cmd/contracts/contracts4juchain/generated"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"

	"math/big"
	"runtime"
	"sync"
	"time"
)

func crossBurnStressCmdV2() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "burnStressV2",
		Short: "burn from ju all of the time",
		Run:   burnTokenATV2,
	}
	addBurnStressV2Flags(cmd)
	return cmd
}

func addBurnStressV2Flags(cmd *cobra.Command) {

	cmd.Flags().IntP("chainID", "w", 97, "cross to dst chain ID")
	cmd.Flags().Int64P("duration", "d", 120, "Continuous stress test time default 120s")
	cmd.Flags().Int64P("interval", "i", 30, "interval time,default 30s")
	cmd.Flags().IntP("maxPending", "p", 3000, "max pending number,default 3000")
	cmd.Flags().StringP("token", "t", "", "token contract address")
	cmd.Flags().StringP("registerAddr", "c", "", "registerAddr contract address")
	cmd.Flags().IntP("repeat", "r", 3000, "repeat number")
	_ = cmd.MarkFlagRequired("registerAddr")
	cmd.Flags().StringP("mnemonic", "m", "", "mnemonic")
	_ = cmd.MarkFlagRequired("mnemonic")
	cmd.Flags().BoolP("approve", "a", false, "approve for burn")
	cmd.Flags().BoolP("view", "v", false, "view burn tx hash")

}

func burnTokenATV2(cmd *cobra.Command, args []string) {
	rpcLaddr, _ := cmd.Flags().GetString("rpc_laddr")
	token, _ := cmd.Flags().GetString("token")
	registerAddr, _ := cmd.Flags().GetString("registerAddr")
	repeat, _ := cmd.Flags().GetInt("repeat")
	chainIDWd, err := cmd.Flags().GetInt("chainID")
	approve, _ := cmd.Flags().GetBool("approve")
	view, _ := cmd.Flags().GetBool("view")
	duration, _ := cmd.Flags().GetInt64("duration")
	interval, _ := cmd.Flags().GetInt64("interval")
	maxPending, _ := cmd.Flags().GetInt("maxPending")
	fmt.Println("registerAddr:", registerAddr)
	fmt.Println("token:", token)
	fmt.Println("approve for burn:", approve)
	if err != nil {
		panic(err)
	}
	mnemonic, err := cmd.Flags().GetString("mnemonic")
	if err != nil {
		panic(err)
	}
	fmt.Println("mnemonic:", mnemonic)
	seed, err := bip39.NewSeedWithErrorChecking(mnemonic, "")
	if err != nil {
		fmt.Println("NewSeedWithErrorChecking with error:", err.Error())
		return
	}

	masterKey, err := bip32.NewMasterKey(seed)
	if err != nil {
		panic(err)
	}
	client, x2EthContracts, x2EthDeployInfo, err := RecoverContractHandler4Dstju(registerAddr, rpcLaddr)
	if err != nil {
		fmt.Println("RecoverContractHandler err:", err)
		return
	}

	var bSender *burnSender
	bSender = &burnSender{
		client:         client,
		bridgeBankIns:  x2EthContracts.BridgeBank,
		bridgeBankAddr: x2EthDeployInfo.BridgeBank.Address,
		tokenAddr:      common.HexToAddress(token),
		amount:         big.NewInt(1),
		chainID2wd:     big.NewInt(int64(chainIDWd)),
		repeat:         repeat,
		recvTxChan:     make(chan *types.Transaction, 5000),
		proceeNum:      50,
		nodeUrl:        rpcLaddr,
		approve:        approve,
		view:           view,
		interval:       interval,
		duration:       duration,
		maxPending:     uint(maxPending),
		hashChan:       make(chan string, 500),
	}

	// 提前生成多个地址(repeat)信息
	bSender.genMutiKeyAddr(masterKey)

	signChan := make(chan *types.Transaction, 10000)
	//循环批量生成签名交易(无限循环)
	go signBurnTxATV2(bSender, signChan)
	//等待签名数量达到一定后往发送管道输送(recvTxChan/runChan)
	go waitSignBurnTxATV2(signChan, bSender)
	//并发发送交易
	go waitSendBurnTxATV2(bSender)
	go pendingTxCount(client, bSender)
	go write2File(bSender)
	processStart := time.Now()

	// 优雅退出监控
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT)
	go func() {
		fmt.Println("Register hook for quiting.")
		// 等待退出信号
		sig := <-sigChan
		fmt.Printf("Received signal: %v\n", sig)
		// 执行清理逻辑，等待交易通道为空
		for {
			fmt.Println("Wait for cleaning tx channel.....")
			time.Sleep(1 * time.Second)
			//先关闭recvchan
			close(bSender.recvTxChan)
			if len(bSender.recvTxChan) == 0 {

				break
			}
		}
		// 退出程序
		os.Exit(0)
	}()

	for {
		fmt.Println("signChan capacity", len(signChan), "recvTxChan capacity", len(bSender.recvTxChan), "runchan:", len(runChan),
			"success send tx:", bSender.sendTxNum, "failed tx:", bSender.sendErrTxNum,
			"pendingTxCount:", bSender.pendingCount, "cycle times:", bSender.cycle, "cost time:", time.Since(processStart))

		time.Sleep(time.Second)
	}
}

func (b *burnSender) genMutiKeyAddr(masterKey *bip32.Key) {
	for i := 0; i < b.repeat; i++ {
		bkey, err := newKeyFromMasterKey(masterKey, TypeEther, bip32.FirstHardenedChild, 0, uint32(i))
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
		signKey, err := crypto.ToECDSA(bkey.Key)
		if err != nil {
			panic(err)
		}

		b.child = append(b.child, &childKeyAddr{addr: addr, key: signKey})
		//b.child <- &childKeyAddr{addr: addr, key: signKey}
	}
}

func signBurnTxATV2(sender *burnSender, sigChan chan *types.Transaction) {
	// 将多个地址交易均分给CPU核心
	numCPUs := runtime.NumCPU()
	txCnt := len(sender.child)
	addrPerGoroutime := txCnt / numCPUs
	fmt.Println("runtime.NumCPU()", numCPUs, "addrPerGoroutime", addrPerGoroutime, "sender num:", txCnt)
	time.Sleep(time.Second * 3)
	for {
		var wg sync.WaitGroup
		//当交易数比CPU核数还要少的情况，一次性处理
		if addrPerGoroutime == 0 {
			wg.Add(1)
			for i := 0; i < len(sender.child); i++ {
				txs, err := SignBurn(sender, sender.child[i].addr, sender.child[i].key)
				if err != nil {
					panic(err)

				}
				for _, tx := range txs {
					sigChan <- tx
				}

			}

		} else {
			for i := 0; i < numCPUs-1; i++ {
				// 按CPU内核数分段处理
				partOfchild := sender.child[i*addrPerGoroutime : (i+1)*addrPerGoroutime]
				wg.Add(1)
				go multiSign(partOfchild, sender, sigChan, &wg)

			}

			partOfchild := sender.child[(numCPUs-1)*addrPerGoroutime:]
			wg.Add(1)
			go multiSign(partOfchild, sender, sigChan, &wg)

		}

		wg.Wait()
		if sender.approve == true {
			//停止签名，完成approve 操作
			fmt.Println("all address aproved signedtx completed")
			return
		}
		//fmt.Println("wait for sign completed++++,cycle:", sender.cycle)
		sender.cycle++
	}

}

func multiSign(childKey []*childKeyAddr, bSender *burnSender, sigChan chan *types.Transaction, wg *sync.WaitGroup) {

	for _, child := range childKey {
		txs, err := SignBurn(bSender, child.addr, child.key)
		if err != nil {
			panic(err)

		}
		//fmt.Println("cycle:", bSender.cycle, "txs:", len(txs))
		//fmt.Println("child addr:", child.addr, "index:", index, "cycle:", bSender.cycle, "txsize:", len(txs), "hash:", txs[0].Hash())
		for _, tx := range txs {
			sigChan <- tx

		}

	}
	wg.Done()
}

func SignBurn(bSender *burnSender, senderAddr common.Address, signKey *ecdsa.PrivateKey) ([]*types.Transaction, error) {
	var err error
	var txs []*types.Transaction
	opts := &bind.CallOpts{
		Pending: false,
		From:    common.Address{},
		Context: context.Background(),
	}

	// 跨链费用计算，只计算一次（可以拿到外层逻辑）
	if bSender.bridgeServiceFee == nil {
		bridgeServiceFee, err := bSender.bridgeBankIns.BridgeServiceFee(opts)
		if nil != err {
			return nil, err
		}
		bSender.bridgeServiceFee = bridgeServiceFee
	}

	//aprove
	if bSender.cycle == 0 && bSender.approve { //第一次需要approve
		auth, err := PrepareAuth(bSender.client, signKey, senderAddr)
		if nil != err {

			return nil, err
		}
		auth.NoSend = true
		tokenInstance, err := generated.NewBridgeToken(bSender.tokenAddr, bSender.client)
		if nil != err {

			panic(err)
		}

		tx, err := tokenInstance.Approve(auth, bSender.bridgeBankAddr, big.NewInt(1e18))
		if nil != err {
			panic(err)
		}
		//fmt.Println("+++++++approve tx:", tx.Hash().Hex(), "nonce:", tx.Nonce(), "from:", senderAddr)
		//err = waitEthTxFinished(bSender.client, tx.Hash(), "Approve")
		//if nil != err {
		//	fmt.Println("waitEthTxFinished:", err)
		//	return nil, err
		//}
		//fmt.Println("+++++++approve tx:", tx.Hash().Hex(), "nonce:", tx.Nonce(), "from:", senderAddr)
		txs = append(txs, tx)
		return txs, nil

	}

	auth, err := PrepareAuth(bSender.client, signKey, senderAddr)
	if nil != err {
		return nil, err
	}

	auth.Value.SetInt64(bSender.bridgeServiceFee.Int64())
	auth.NoSend = true
	tx, err := bSender.bridgeBankIns.BurnBridgeTokens(auth, bSender.chainID2wd, senderAddr, bSender.tokenAddr, bSender.amount)
	if nil != err {
		return nil, err
	}
	txs = append(txs, tx)
	return txs, nil
}

func waitSignBurnTxATV2(sigChan chan *types.Transaction, sender *burnSender) {
	var startRecord time.Time
	var sendCost time.Duration
	var durationTime = time.Now()
	for {
		var count int
		if len(sender.recvTxChan) == 0 {
			if !startRecord.IsZero() { //控制发送速度
				sendCost = time.Now().Sub(startRecord)
				if sendCost < time.Millisecond*800 { //每个压测批次间隔800毫秒
					continue
				}
				//每个压测持续时间到达后休息时间是interval
				if time.Now().Sub(durationTime) > time.Second*time.Duration(sender.duration) {
					time.Sleep(time.Second * time.Duration(sender.interval))
					//reset durationTime
					durationTime = time.Now()
				}
			}

			for tx := range sigChan {
				count++
				sender.recvTxChan <- tx
				// 按批次处理（数量为测试目标交易地址数量）
				if count >= sender.repeat {
					// 按并发数量触发执行开关（CPU核数）
					startRecord = time.Now()

					for i := 0; i < sender.proceeNum; i++ {
						runChan <- true
					}

					count = 0
					break

				}

			}
		}

	}

}

func pendingTxCount(client *ethclient.Client, sender *burnSender) {
	for {
		time.Sleep(time.Millisecond * 400)
		pendingCount, err := client.PendingTransactionCount(context.Background())
		if err == nil {
			sender.pendingCount = pendingCount
			continue
		}
	}
}
func checkPendingTx(sender *burnSender) {
	for {

		if sender.pendingCount > sender.maxPending && sender.pendingCount > 0 {
			fmt.Println("current pending", sender.pendingCount, "maxPending:", sender.maxPending)
			time.Sleep(time.Second)
			continue
		}
		return

	}
}
func waitSendBurnTxATV2(sender *burnSender) {

	if sender.proceeNum == 0 {
		panic("must set proceeNum")
	}
	for i := 0; i < sender.proceeNum; i++ {
		go func(index int) {
			client, err := ethclient.Dial(sender.nodeUrl)
			if err != nil {
				fmt.Println(err)
				panic(err)
			}

			for {
				<-runChan
			out:
				for {

					select {

					case tx := <-sender.recvTxChan:
						checkPendingTx(sender)
						err := client.SendTransaction(context.Background(), tx)
						if err != nil {
							signer := types.NewEIP155Signer(tx.ChainId())
							from, _ := types.Sender(signer, tx)
							fmt.Println("Failed to sendTxAT with err:", err, "will retry...:", from)

							nMutex.Lock()
							delete(addr2NonceOnly4test, from)
							nMutex.Unlock()
							atomic.AddInt64(&sender.sendErrTxNum, 1)
							continue
						}
						atomic.AddInt64(&sender.sendTxNum, 1)
						if sender.view {
							signer := types.NewEIP155Signer(tx.ChainId())
							from, _ := types.Sender(signer, tx)
							//fmt.Println("send tx at:", tx.Hash().Hex(), "processNum:", index, "tx.Nonce:", tx.Nonce())
							sender.hashChan <- fmt.Sprintf("send tx hash:%s,from:%s,tx.Nonce:%d\n", tx.Hash().Hex(), from, tx.Nonce())
						}

					default:
						//fmt.Println("waitSendBurnTxATV2 recvTxChan:", len(sender.recvTxChan))
						if len(sender.recvTxChan) == 0 {
							if sender.approve && sender.cycle == 0 {
								fmt.Printf("all approve tx send completed goroutine num:%d quit...\n", index)
								time.Sleep(time.Second * 3)
								os.Exit(0)
							}
							break out
						}

					}
				}

			}
		}(i)
	}
}

func write2File(sender *burnSender) {
	file, err := os.Create("burnStress.txt")
	defer file.Close()
	if err != nil {
		panic(err)
	}
	writer := bufio.NewWriter(file)

	for {
		content := <-sender.hashChan
		_, _ = writer.WriteString(content)
	}
}
