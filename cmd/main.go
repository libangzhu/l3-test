package main

import (
	"fmt"
	"github.com/spf13/cobra"
	"github.com/zhengjunhe/l3-test/cmd/buildflags"
	"net/http"
	"os"
	"strings"
)

var (
	rootCmd = &cobra.Command{
		Use:   "l3Test" + "-cli",
		Short: "l3Test" + "client tools",
	}
)

func init() {
	rootCmd.AddCommand(
		transferCmd(),  //coins 1->multi  step 1
		crossBurnCmd(), //evm burn-withdraw 1->multi
		transferStressCmd(),
		crossBurnStressCmd(),
		tokenCmd(),             // step 2
		crossBurnStressCmdV2(), // step 3
		checkNonceCmd(),
	)
}

func testTLS(RPCAddr string) string {
	rpcaddr := RPCAddr
	if strings.HasPrefix(rpcaddr, "https://") {
		return RPCAddr
	}
	if !strings.HasPrefix(rpcaddr, "http://") {
		return RPCAddr
	}
	//test tls ok
	if rpcaddr[len(rpcaddr)-1] != '/' {
		rpcaddr += "/"
	}
	rpcaddr += "test"
	resp, err := http.Get(rpcaddr)
	if err != nil {
		return "https://" + RPCAddr[7:]
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		return RPCAddr
	}
	return "https://" + RPCAddr[7:]
}

// run :
func run(RPCAddr, NodeAddr, RegisterAddr string, chainID int64) {
	//test tls is enable
	//RPCAddr = testTLS(RPCAddr)

	rootCmd.PersistentFlags().String("rpc_laddr", RPCAddr, "http url")
	rootCmd.PersistentFlags().Int64("signChainID", chainID, "sign chain id")
	//rootCmd.PersistentFlags().String("node_addr", NodeAddr, "bsc node url")
	//rootCmd.PersistentFlags().String("eth_chain_name", "Binance", "chain name")
	//rootCmd.PersistentFlags().String("register_addr", RegisterAddr, "contract register address")

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func main() {
 
	if buildflags.RPCAddr == "" {
		buildflags.RPCAddr = "http://localhost:8545"
	}

	if buildflags.SignChainID == 0 {
		buildflags.SignChainID = 202588
		chainID = buildflags.SignChainID
	}

	rootCmd.PersistentFlags().String("rpc_laddr", buildflags.RPCAddr, "http url")
	rootCmd.PersistentFlags().Int64("signChainID", buildflags.SignChainID, "sign chain id")

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

}
