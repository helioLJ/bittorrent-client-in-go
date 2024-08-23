package main

import (
	"bittorrent-client/internal/file"
	"bittorrent-client/internal/torrent"
	"bittorrent-client/internal/tracker"
	"bittorrent-client/pkg/logger"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	// Initialize logger
	logger.InitLogger(os.Stdout)

	// Check command-line arguments
	if len(os.Args) < 2 {
		logger.Error("Usage: %s <torrent_file_or_magnet_link> [output_file]", os.Args[0])
		os.Exit(1)
	}

	input := os.Args[1]
	outputFile := "output"
	if len(os.Args) > 2 {
		outputFile = os.Args[2]
	}

	var metaInfo *torrent.MetaInfo
	var err error

	// Check if the input is a magnet link
	if strings.HasPrefix(input, "magnet:") {
		logger.Info("Detected magnet link: %s", input)
		magnetInfo, err := torrent.ParseMagnetLink(input)
		if err != nil {
			logger.Error("Failed to parse magnet link: %v", err)
			os.Exit(1)
		}
		metaInfo = magnetInfo.ConvertToMetaInfo()
		if metaInfo.Announce == "" {
			logger.Warn("No trackers found in magnet link. DHT may be required for peer discovery.")
		}
	} else {
		// Read the .torrent file
		torrentData, err := os.ReadFile(input)
		if err != nil {
			logger.Error("Failed to read torrent file: %v", err)
			os.Exit(1)
		}

		// Parse the MetaInfo from the torrent file
		metaInfo, err = torrent.ParseMetaInfo(torrentData)
		if err != nil {
			logger.Error("Failed to parse torrent file: %v", err)
			os.Exit(1)
		}
	}

	// Create a multi-tracker instance
	peerID := torrent.GeneratePeerID()
	infoHash, err := metaInfo.InfoHash()
	if err != nil {
		logger.Error("Failed to generate info hash: %v", err)
		os.Exit(1)
	}

	multiTracker := tracker.NewMultiTracker(peerID, infoHash)

	// Add the main tracker from the torrent file
	if metaInfo.Announce != "" {
		trackerURL, err := url.Parse(metaInfo.Announce)
		if err == nil {
			multiTracker.AddTracker(tracker.NewTracker(trackerURL, peerID, infoHash))
		}
	}

	// Add fallback trackers
	fallbackTrackers := []string{
		"udp://tracker.opentrackr.org:1337/announce",
		"udp://tracker.openbittorrent.com:6969/announce",
		"udp://tracker.internetwarriors.net:1337/announce",
	}
	for _, fallbackTracker := range fallbackTrackers {
		fallbackURL, err := url.Parse(fallbackTracker)
		if err == nil {
			multiTracker.AddTracker(tracker.NewTracker(fallbackURL, peerID, infoHash))
		}
	}

	// Create a new FileManager
	fm := file.NewFileManager(".")
	fm.SetPieceSize(int64(metaInfo.Info.PieceLength)) // Set the piece size

	// Create the necessary files for the torrent
	var lengths []int64
	if len(metaInfo.Info.Files) > 0 {
		lengths = make([]int64, len(metaInfo.Info.Files))
		for i, file := range metaInfo.Info.Files {
			lengths[i] = file.Length
		}
	} else {
		lengths = []int64{metaInfo.Info.Length}
	}

	err = fm.CreateFiles(metaInfo.Info.Name, lengths)
	if err != nil {
		logger.Error("Failed to create files: %v", err)
		os.Exit(1)
	}

	// Create a new Torrent instance with the multi-tracker
	t, err := torrent.NewTorrent(metaInfo, multiTracker)
	if err != nil {
		logger.Error("Failed to create torrent: %v", err)
		os.Exit(1)
	}

	// Set the FileManager in the Torrent instance
	t.SetFileManager(fm)

	// Create a context that we can cancel
	ctx, cancel := context.WithCancel(context.Background())

	// Handle SIGINT (Ctrl+C) gracefully
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		<-c
		logger.Info("Received interrupt signal. Shutting down...")
		cancel()
	}()

	// Start the download
	logger.Info("Starting download for: %s", metaInfo.Info.Name)
	
	// Add a timeout for the download
	downloadCtx, downloadCancel := context.WithTimeout(ctx, 1*time.Hour)
	defer downloadCancel()

	err = t.Download(downloadCtx)
	if err != nil {
		if err == context.Canceled {
			logger.Info("Download was cancelled")
		} else if err == context.DeadlineExceeded {
			logger.Info("Download timed out")
		} else {
			logger.Error("Download failed: %v", err)
			// Attempt to use DHT for peer discovery if tracker fails
			logger.Info("Attempting to use DHT for peer discovery...")
			err = t.UseDHT(downloadCtx)
			if err != nil {
				logger.Error("DHT peer discovery failed: %v", err)
			}
		}
	} else {
		logger.Info("Download completed successfully")
	}

	// Close the FileManager
	err = fm.Close()
	if err != nil {
		logger.Error("Failed to close files: %v", err)
	}

	outputPath := filepath.Join(".", outputFile)
	fmt.Printf("Downloaded file saved to: %s\n", outputPath)
}