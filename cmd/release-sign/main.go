// release-sign makes the ed25519 key pair for signing releases, and signs checksums.txt in CI.
//
//	go run ./cmd/release-sign keygen        prints PUBLIC=<base64> and PRIVATE=<base64>
//	RELEASE_SIGNING_KEY=<private> go run ./cmd/release-sign sign checksums.txt > checksums.txt.sig
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "keygen" {
		pub, priv, err := ed25519.GenerateKey(nil)
		if err != nil {
			panic(err)
		}
		fmt.Printf("PUBLIC=%s\nPRIVATE=%s\n", base64.StdEncoding.EncodeToString(pub), base64.StdEncoding.EncodeToString(priv))
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "sign" {
		key, err := base64.StdEncoding.DecodeString(os.Getenv("RELEASE_SIGNING_KEY"))
		if err != nil || len(key) != ed25519.PrivateKeySize {
			fmt.Fprintln(os.Stderr, "RELEASE_SIGNING_KEY must be the base64 private key from keygen")
			os.Exit(1)
		}
		data, err := os.ReadFile(os.Args[2])
		if err != nil {
			panic(err)
		}
		fmt.Println(base64.StdEncoding.EncodeToString(ed25519.Sign(ed25519.PrivateKey(key), data)))
		return
	}
	fmt.Fprintln(os.Stderr, "usage: release-sign keygen | release-sign sign FILE")
	os.Exit(2)
}
