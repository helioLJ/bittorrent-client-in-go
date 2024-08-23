package main

import (
	"bittorrent-client/internal/torrent"
	"bittorrent-client/internal/tracker"
	"bittorrent-client/pkg/logger"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

func main() {
	// Initialize logger
	logger.InitLogger(os.Stdout)

	// Check command-line arguments
	if len(os.Args) < 2 {
		logger.Error("Usage: %s <torrent_file> [output_file]", os.Args[0])
		os.Exit(1)
	}

	torrentFile := os.Args[1]
	outputFile := "output"
	if len(os.Args) > 2 {
		outputFile = os.Args[2]
	}

	// Read the .torrent file
	torrentData, err := os.ReadFile(torrentFile)
	if err != nil {
		logger.Error("Failed to read torrent file: %v", err)
		os.Exit(1)
	}

	// Parse the MetaInfo from the torrent file
	metaInfo, err := torrent.ParseMetaInfo(torrentData)
	if err != nil {
		logger.Error("Failed to parse torrent file: %v", err)
		os.Exit(1)
	}

	// Create a tracker instance
	trackerURL, err := url.Parse(metaInfo.Announce)
	if err != nil {
		logger.Error("Failed to parse tracker URL: %v", err)
		os.Exit(1)
	}

	peerID := torrent.GeneratePeerID()
	infoHash, err := metaInfo.InfoHash()
	if err != nil {
		logger.Error("Failed to generate info hash: %v", err)
		os.Exit(1)
	}

	trackerInstance := tracker.NewTracker(trackerURL, peerID, infoHash)

	// Create a new Torrent instance
	t, err := torrent.NewTorrent(metaInfo, trackerInstance)
	if err != nil {
		logger.Error("Failed to create torrent: %v", err)
		os.Exit(1)
	}

	// Create a context that we can cancel
	ctx, cancel := context.WithCancel(context.Background())

	// Handle SIGINT (Ctrl+C) gracefully
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		logger.Info("Received interrupt signal. Shutting down...")
		cancel()
	}()

	// Start the download
	logger.Info("Starting download for: %s", metaInfo.Info.Name)
	err = t.Download(ctx)
	if err != nil {
		if err == context.Canceled {
			logger.Info("Download was cancelled")
		} else {
			logger.Error("Download failed: %v", err)
		}
	} else {
		logger.Info("Download completed successfully")
	}

	// TODO: Implement saving the downloaded data to the output file
	outputPath := filepath.Join(".", outputFile)
	fmt.Printf("Downloaded file will be saved to: %s\n", outputPath)
}