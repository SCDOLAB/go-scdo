/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package util

import (
	"crypto/ecdsa"
	"fmt"
	"io"
	"os"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/common/hexutil"
	"github.com/scdoproject/go-scdo/crypto"
	"github.com/spf13/cobra"
)

// GetGenerateKeyPairCmd represents the generateKeyPair command
func GetGenerateKeyPairCmd(name string) (cmds *cobra.Command) {
	var shard *uint
	var outPath *string

	var generateKeyPairCmd = &cobra.Command{
		Use:   "key",
		Short: "generate a key pair with specified shard number",
		Long: "generate a key pair and print them with hex values\n For example:\n" + name + " key --shard 1\n" +
			name + " key --shard 1 --out wallet.key",
		Run: func(cmd *cobra.Command, args []string) {
			publicKey, privateKey, err := GenerateKey(*shard)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return
			}

			path := ""
			if outPath != nil {
				path = *outPath
			}
			if err := writeKeyPair(os.Stdout, os.Stderr, publicKey, privateKey, path); err != nil {
				fmt.Fprintln(os.Stderr, err)
			}
		},
	}

	shard = generateKeyPairCmd.Flags().UintP("shard", "", 0, "shard number")
	outPath = generateKeyPairCmd.Flags().String("out", "", "write the private key to this file (mode 0600) instead of printing it")

	return generateKeyPairCmd
}

// writeKeyPair prints the account and either the private key or a path to a 0600 file.
// The warning goes to stderr so scripts that parse "private key:" on stdout still work.
func writeKeyPair(stdout, stderr io.Writer, publicKey *common.Address, privateKey *ecdsa.PrivateKey, outPath string) error {
	fmt.Fprintln(stderr, "warning: this private key controls the account. Anyone who reads it can spend the funds.")
	keyHex := hexutil.BytesToHex(crypto.FromECDSA(privateKey))
	fmt.Fprintf(stdout, "Account:  %s\n", publicKey.Hex())
	if outPath != "" {
		if err := common.SaveFile(outPath, []byte(keyHex+"\n")); err != nil {
			return err
		}
		if err := os.Chmod(outPath, 0600); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "private key written to %s (mode 0600)\n", outPath)
		return nil
	}
	fmt.Fprintf(stdout, "private key: %s\n", keyHex)
	return nil
}

// GenerateKey generate key by shard
func GenerateKey(shard uint) (*common.Address, *ecdsa.PrivateKey, error) {
	var publicKey *common.Address
	var privateKey *ecdsa.PrivateKey
	var err error
	if shard > common.ShardCount {
		return nil, nil, fmt.Errorf("not supported shard number, shard number should be [0, %d]", common.ShardCount)
	} else if shard == 0 {
		shard := crypto.RandomShard()
		publicKey, privateKey, err = crypto.GenerateKeyPair(shard)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to generate the key pair: %s", err)
		}
	} else {
		publicKey, privateKey = crypto.MustGenerateShardKeyPair(shard)
	}

	return publicKey, privateKey, nil
}
