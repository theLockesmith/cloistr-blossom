package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/hashing"
)

func main() {
	sk := nostr.GeneratePrivateKey()
	pk, _ := nostr.GetPublicKey(sk)
	
	// Create a minimal valid PNG (1x1 transparent pixel)
	// PNG header + IHDR + IDAT + IEND
	content := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG signature
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52, // IHDR chunk
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, // 1x1 dimensions
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4, // 8-bit RGBA
		0x89, 0x00, 0x00, 0x00, 0x0A, 0x49, 0x44, 0x41, // IDAT chunk
		0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00,
		0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE, // IEND chunk
		0x42, 0x60, 0x82,
	}
	
	hash, _ := hashing.Hash(content)
	
	fmt.Printf("Pubkey: %s\n", pk)
	fmt.Printf("Content hash: %s\n", hash)
	fmt.Printf("Content size: %d bytes\n", len(content))
	
	// Create auth event
	ev := &nostr.Event{
		CreatedAt: nostr.Now(),
		Kind:      24242,
		Tags: nostr.Tags{
			{"t", "upload"},
			{"x", hash},
			{"expiration", fmt.Sprintf("%d", time.Now().Add(time.Hour).Unix())},
		},
	}
	ev.Sign(sk)
	
	evBytes, _ := json.Marshal(ev)
	auth := base64.StdEncoding.EncodeToString(evBytes)
	
	// Upload
	req, _ := http.NewRequest("PUT", "https://files.cloistr.xyz/upload", bytes.NewReader(content))
	req.Header.Set("Authorization", "Nostr "+auth)
	req.Header.Set("Content-Type", "image/png")
	
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("Upload failed: %v\n", err)
		return
	}
	defer resp.Body.Close()
	
	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("Response status: %d\n", resp.StatusCode)
	fmt.Printf("Response body: %s\n", string(body))
}
