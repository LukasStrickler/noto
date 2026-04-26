package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}
	
	ctx := context.Background()
	
	switch os.Args[1] {
	case "start":
		if err := cmdStart(ctx, os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "stop":
		if err := cmdStop(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "pause":
		if err := cmdPause(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "resume":
		if err := cmdResume(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "level":
		if err := cmdLevel(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "status":
		if err := cmdStatus(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "audio":
		if err := cmdAudio(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprint(os.Stderr, `Noto Audio Capture Helper

Usage:
  noto-capture start [--sources=microphone,system_audio] [--sample-rate=44100]
  noto-capture stop
  noto-capture pause
  noto-capture resume
  noto-capture level
  noto-capture status
  noto-capture audio
  noto-capture help

Commands:
  start      Start recording with specified sources and sample rate
  stop       Stop recording and return audio metadata
  pause      Pause recording
  resume     Resume paused recording
  level      Get current audio levels (left, right, ambient)
  status     Get current recording status
  audio      Get captured audio data (base64 encoded)
  help       Show this help message

Options:
  --sources      Comma-separated list of audio sources (default: microphone,system_audio)
  --sample-rate  Sample rate in Hz (default: 44100)

Audio Sources:
  microphone     Capture from microphone (local_speaker channel)
  system_audio   Capture system audio (participants channel)

Examples:
  noto-capture start --sources=microphone --sample-rate=44100
  noto-capture stop
  noto-capture level
`)
}

func cmdStart(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	sources := fs.String("sources", "microphone,system_audio", "comma-separated sources")
	sampleRate := fs.Int("sample-rate", 44100, "sample rate in Hz")
	
	if err := fs.Parse(args); err != nil {
		return err
	}
	
	sourceList := parseSources(*sources)
	
	client, err := NewIPCClient()
	if err != nil {
		return fmt.Errorf("could not create IPC client: %w", err)
	}
	defer client.Close()
	
	result, err := client.Start(ctx, sourceList, *sampleRate)
	if err != nil {
		return fmt.Errorf("start failed: %w", err)
	}
	
	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(data))
	return nil
}

func cmdStop(ctx context.Context) error {
	client, err := NewIPCClient()
	if err != nil {
		return fmt.Errorf("could not create IPC client: %w", err)
	}
	defer client.Close()
	
	result, err := client.Stop(ctx)
	if err != nil {
		return fmt.Errorf("stop failed: %w", err)
	}
	
	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(data))
	return nil
}

func cmdPause(ctx context.Context) error {
	client, err := NewIPCClient()
	if err != nil {
		return fmt.Errorf("could not create IPC client: %w", err)
	}
	defer client.Close()
	
	if err := client.Pause(ctx); err != nil {
		return fmt.Errorf("pause failed: %w", err)
	}
	
	fmt.Println(`{"status": "paused"}`)
	return nil
}

func cmdResume(ctx context.Context) error {
	client, err := NewIPCClient()
	if err != nil {
		return fmt.Errorf("could not create IPC client: %w", err)
	}
	defer client.Close()
	
	if err := client.Resume(ctx); err != nil {
		return fmt.Errorf("resume failed: %w", err)
	}
	
	fmt.Println(`{"status": "recording"}`)
	return nil
}

func cmdLevel(ctx context.Context) error {
	client, err := NewIPCClient()
	if err != nil {
		return fmt.Errorf("could not create IPC client: %w", err)
	}
	defer client.Close()
	
	result, err := client.GetAudioLevel(ctx)
	if err != nil {
		return fmt.Errorf("getAudioLevel failed: %w", err)
	}
	
	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(data))
	return nil
}

func cmdStatus(ctx context.Context) error {
	client, err := NewIPCClient()
	if err != nil {
		return fmt.Errorf("could not create IPC client: %w", err)
	}
	defer client.Close()
	
	level, err := client.GetAudioLevel(ctx)
	if err != nil {
		return fmt.Errorf("status check failed: %w", err)
	}
	
	status := map[string]any{
		"state":       "ready",
		"audio_level": level,
		"timestamp":    time.Now().UTC().Format(time.RFC3339),
	}
	
	data, _ := json.MarshalIndent(status, "", "  ")
	fmt.Println(string(data))
	return nil
}

func cmdAudio(ctx context.Context) error {
	client, err := NewIPCClient()
	if err != nil {
		return fmt.Errorf("could not create IPC client: %w", err)
	}
	defer client.Close()
	
	result, err := client.GetCapturedAudio(ctx)
	if err != nil {
		return fmt.Errorf("getCapturedAudio failed: %w", err)
	}
	
	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(data))
	return nil
}

func parseSources(s string) []string {
	if s == "" {
		return []string{"microphone"}
	}
	
	var sources []string
	for _, part := range splitCommas(s) {
		part = trimQuotes(part)
		if part == "microphone" || part == "system_audio" {
			sources = append(sources, part)
		}
	}
	
	if len(sources) == 0 {
		sources = []string{"microphone"}
	}
	
	return sources
}

func splitCommas(s string) []string {
	var result []string
	var current []byte
	
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			if len(current) > 0 {
				result = append(result, string(current))
				current = nil
			}
		} else {
			current = append(current, s[i])
		}
	}
	
	if len(current) > 0 {
		result = append(result, string(current))
	}
	
	return result
}

func trimQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') ||
			(s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
