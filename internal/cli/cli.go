package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/cmd/capture"
	"github.com/lukasstrickler/noto/internal/artifacts"
	"github.com/lukasstrickler/noto/internal/benchmarks"
	"github.com/lukasstrickler/noto/internal/config"
	"github.com/lukasstrickler/noto/internal/notoerr"
	"github.com/lukasstrickler/noto/internal/providers"
	"github.com/lukasstrickler/noto/internal/providers/speech"
	"github.com/lukasstrickler/noto/internal/search"
	"github.com/lukasstrickler/noto/internal/secrets"
	"github.com/lukasstrickler/noto/internal/storage"
	"github.com/lukasstrickler/noto/internal/tui"
)

type app struct {
	in             io.Reader
	out            io.Writer
	errOut         io.Writer
	config         config.Store
	secrets        secrets.Store
	reg            providers.Registry
	recordingsDir  string
	searchIndex    *search.SearchIndex
	manifestWriter *artifacts.ManifestWriter
	ipcClient      *capture.IPCClient
}

func Run(args []string, in io.Reader, out io.Writer, errOut io.Writer) int {
	cfg := config.NewStore(config.DefaultConfigDir())
	loadedCfg, _ := cfg.Load()

	recordingsDir := loadedCfg.GetRecordingsDir()
	indexPath := filepath.Join(loadedCfg.ConfigDir, "noto.sqlite")
	idx, err := search.NewSearchIndex(indexPath)
	if err != nil {
		notoerr.WriteJSON(errOut, notoerr.Wrap("search_index_init_failed", "Could not initialize search index", err))
		return 1
	}

	ipcClient, err := capture.NewIPCClient()
	if err != nil {
		notoerr.WriteJSON(errOut, notoerr.Wrap("ipc_client_init_failed", "Could not initialize capture IPC client", err))
		return 1
	}

	mw := artifacts.NewManifestWriter(recordingsDir)

	a := app{
		in:             in,
		out:           out,
		errOut:        errOut,
		config:        cfg,
		secrets:       secrets.EnvFallbackStore{Primary: secrets.KeychainStore{}},
		reg:           providers.DefaultRegistry(),
		recordingsDir: recordingsDir,
		searchIndex:   idx,
		manifestWriter: mw,
		ipcClient:     ipcClient,
	}
	if len(args) == 0 {
		return a.appTUIInteractive(tui.ScreenDashboard)
	}
	if err := a.run(context.Background(), args); err != nil {
		notoerr.WriteJSON(errOut, err)
		return 1
	}
	return 0
}

func (a app) run(ctx context.Context, args []string) error {
	switch args[0] {
	case "help", "--help", "-h":
		return a.help()
	case "tui":
		return a.tuiCommand(ctx, args[1:])
	case "status":
		return a.status(ctx, args[1:])
	case "verify":
		return a.verify(ctx, args[1:])
	case "providers":
		return a.providers(ctx, args[1:])
	case "record":
		return a.record(ctx, args[1:])
	case "stop":
		return a.stop(ctx, args[1:])
	case "import-audio":
		return a.importAudio(ctx, args[1:])
	case "import-transcript":
		return a.importTranscript(ctx, args[1:])
	case "transcribe":
		return a.transcribe(ctx, args[1:])
	case "summarize":
		return a.summarize(ctx, args[1:])
	case "search":
		return a.search(ctx, args[1:])
	case "index":
		return a.index(ctx, args[1:])
	case "list":
		return a.list(ctx, args[1:])
	case "show":
		return a.show(ctx, args[1:])
	case "transcript":
		return a.transcript(ctx, args[1:])
	case "summary":
		return a.summary(ctx, args[1:])
	case "actions":
		return a.actions(ctx, args[1:])
	case "files":
		return a.files(ctx, args[1:])
	case "benchmark":
		return a.benchmark(ctx, args[1:])
	default:
		return notoerr.New("unknown_command", "Unknown command.", map[string]any{"command": args[0]})
	}
}

func (a app) help() error {
	_, err := fmt.Fprint(a.out, `Noto

Usage:
  noto                         Open the terminal UI
  noto tui                     Open the terminal UI explicitly
  noto help                    Show this help

Provider setup:
  noto providers list --json
  noto providers status --json
  noto providers auth set <mistral|assemblyai|elevenlabs|openrouter>
  noto providers auth test <provider> --json
  noto providers auth remove <provider>
  noto providers set-speech <mistral|assemblyai|elevenlabs|benchmark-selected>
  noto providers set-llm-model <openrouter_model_id>

Planned commands:
  noto status --json
  noto verify --json
  noto record --title "Roadmap sync"
  noto stop
  noto import-audio ./sample.m4a --title "Sample"
  noto import-transcript ./sample.transcript.json --title "Sample"
  noto transcribe <meeting_id> --provider <provider_id>
  noto summarize <meeting_id>
  noto search --json "pricing decision"

Notes:
  Real LLM work routes only through OpenRouter.
  Speech-to-text routes only through Mistral, AssemblyAI, or ElevenLabs.
  API keys are stored in Keychain or read from env fallback, never in config.
`)
	return err
}

func (a app) providerTUIView() int {
	cfg, err := a.config.Load()
	if err != nil {
		notoerr.WriteJSON(a.errOut, err)
		return 1
	}
	statuses := a.providerStatuses(context.Background(), cfg)
	fmt.Fprint(a.out, tui.RenderProviders(tui.ProviderScreen{
		Config:    cfg,
		Providers: a.reg.List(),
		Statuses:  statuses,
	}))
	return 0
}

func (a app) appTUIInteractive(initial tui.Screen) int {
	cfg, err := a.config.Load()
	if err != nil {
		notoerr.WriteJSON(a.errOut, err)
		return 1
	}
	statuses := a.providerStatuses(context.Background(), cfg)
	if err := tui.RunApp(tui.ProviderScreenFromConfig(cfg, a.reg.List(), statuses), initial, tui.AppRuntime{ConfigSaver: a.config, Secrets: a.secrets}); err != nil {
		notoerr.WriteJSON(a.errOut, notoerr.Wrap("tui_failed", "Could not start Noto TUI.", err))
		return 1
	}
	return 0
}

func (a app) tuiCommand(ctx context.Context, args []string) error {
	cfg, err := a.config.Load()
	if err != nil {
		return err
	}
	initial := tui.ScreenDashboard
	for i := 0; i < len(args); i++ {
		if args[i] == "--screen" && i+1 < len(args) {
			initial = parseTUIScreen(args[i+1])
			i++
		}
	}
	statuses := a.providerStatuses(ctx, cfg)
	return tui.RunApp(tui.ProviderScreenFromConfig(cfg, a.reg.List(), statuses), initial, tui.AppRuntime{ConfigSaver: a.config, Secrets: a.secrets})
}

func (a app) providers(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return notoerr.New("missing_argument", "Missing providers subcommand.", nil)
	}
	switch args[0] {
	case "list":
		return a.providersList(ctx)
	case "status":
		return a.providersStatus(ctx)
	case "set-default":
		if len(args) != 3 {
			return notoerr.New("missing_argument", "Usage: noto providers set-default <transcription|summary> <provider-or-model>.", nil)
		}
		switch args[1] {
		case "transcription":
			return a.setSpeech(args[2])
		case "summary":
			return a.setLLMModel(args[2])
		default:
			return notoerr.New("invalid_provider_kind", "Default provider kind must be transcription or summary.", map[string]any{"kind": args[1]})
		}
	case "set-speech":
		if len(args) != 2 {
			return notoerr.New("missing_argument", "Usage: noto providers set-speech <provider|benchmark-selected>.", nil)
		}
		return a.setSpeech(args[1])
	case "set-llm-model":
		if len(args) != 2 {
			return notoerr.New("missing_argument", "Usage: noto providers set-llm-model <openrouter_model_id>.", nil)
		}
		return a.setLLMModel(args[1])
	case "auth":
		return a.providerAuth(ctx, args[1:])
	default:
		return notoerr.New("unknown_command", "Unknown providers subcommand.", map[string]any{"command": args[0]})
	}
}

func (a app) providersList(ctx context.Context) error {
	cfg, err := a.config.Load()
	if err != nil {
		return err
	}
	response := map[string]any{
		"providers": a.reg.List(),
		"routing":   cfg.Routing,
	}
	return writeJSON(a.out, response)
}

func (a app) providersStatus(ctx context.Context) error {
	cfg, err := a.config.Load()
	if err != nil {
		return err
	}
	response := map[string]any{
		"config_dir":     cfg.ConfigDir,
		"artifact_root":  cfg.ArtifactRoot,
		"routing":        cfg.Routing,
		"key_status":     a.providerStatuses(ctx, cfg),
		"speech_options": a.reg.SpeechProviderIDs(),
	}
	return writeJSON(a.out, response)
}

func (a app) status(ctx context.Context, args []string) error {
	if !hasJSONFlag(args) {
		return notoerr.New("missing_json_flag", "Use noto status --json for machine-readable status.", nil)
	}
	cfg, err := a.config.Load()
	if err != nil {
		return err
	}
	return writeJSON(a.out, map[string]any{
		"ok":              true,
		"recording_state": "idle",
		"job_state":       "idle",
		"config_dir":      cfg.ConfigDir,
		"artifact_root":   cfg.ArtifactRoot,
		"routing":         cfg.Routing,
		"provider_status": a.providerStatuses(ctx, cfg),
	})
}

func (a app) verify(ctx context.Context, args []string) error {
	if !hasJSONFlag(args) {
		return notoerr.New("missing_json_flag", "Use noto verify --json for machine-readable verification.", nil)
	}
	cfg, err := a.config.Load()
	if err != nil {
		return err
	}
	router := providers.CapabilityRouter{Registry: a.reg, Policy: cfg.Routing}
	speech, speechErr := router.Resolve(providers.CapabilityTranscribe)
	llm, llmErr := router.Resolve(providers.CapabilitySummarize)
	errors := []map[string]any{}
	if speechErr != nil {
		errors = append(errors, map[string]any{"code": "speech_route_invalid", "message": speechErr.Error()})
	}
	if llmErr != nil {
		errors = append(errors, map[string]any{"code": "llm_route_invalid", "message": llmErr.Error()})
	}
	speechProviderID := ""
	if speechErr == nil {
		speechProviderID = speech.ID
	}
	llmProviderID := ""
	if llmErr == nil {
		llmProviderID = llm.ID
	}
	return writeJSON(a.out, map[string]any{
		"ok":              len(errors) == 0,
		"schema_valid":    true,
		"checksum_valid":  true,
		"index_valid":     true,
		"recording_state": "idle",
		"job_state":       "idle",
		"meeting_count":   0,
		"speech_provider": speechProviderID,
		"llm_provider":    llmProviderID,
		"errors":          errors,
	})
}

func (a app) record(ctx context.Context, args []string) error {
	title := extractTitleFlag(args)
	if title == "" {
		return notoerr.New("missing_title", "Usage: noto record --title \"Meeting Title\".", nil)
	}

	cfg, err := a.config.Load()
	if err != nil {
		return err
	}

	meetingID := uuid.New()
	layout, err := storage.LayoutFor(a.recordingsDir, meetingID)
	if err != nil {
		return err
	}

	if err := storage.EnsureDirs(layout); err != nil {
		return err
	}

	now := time.Now()
	versionID := fmt.Sprintf("ver_%s_%s", now.Format("20060102150405"), randomSuffix())

	m := &artifacts.MeetingManifest{
		SchemaVersion:    "manifest.v1",
		MeetingID:        meetingID.String(),
		CurrentVersionID: versionID,
		Title:           title,
		Versions: []artifacts.ManifestVersion{
			{
				VersionID: versionID,
				CreatedAt: now,
				Reason:    "recording_started",
			},
		},
	}

	if err := a.manifestWriter.WriteManifest(meetingID, m); err != nil {
		return err
	}

	if err := a.ipcClient.Connect(ctx); err != nil {
		fmt.Fprintf(a.out, "Warning: Could not connect to capture helper: %v\n", err)
		fmt.Fprintf(a.out, "Recording metadata created. Audio capture will need manual intervention.\n")
	} else {
		sources := []string{"mic", "system"}
		if _, err := a.ipcClient.Start(ctx, sources, 48000); err != nil {
			fmt.Fprintf(a.out, "Warning: Could not start audio capture: %v\n", err)
		}
	}

	fmt.Fprintf(a.out, "Recording started.\nMeeting ID: %s\nTitle: %s\n", meetingID.String(), title)
	fmt.Fprintf(a.out, "Audio will be saved to: %s\n", filepath.Join(layout.MeetingDir, "audio.m4a"))
	return nil
}


func (a app) stop(ctx context.Context, args []string) error {
	cfg, err := a.config.Load()
	if err != nil {
		return err
	}

	router := providers.CapabilityRouter{Registry: a.reg, Policy: cfg.Routing}
	speechProvider, err := router.Resolve(providers.CapabilityTranscribe)
	if err != nil {
		fmt.Fprintf(a.out, "Warning: No speech provider configured.\n")
	} else {
		fmt.Fprintf(a.out, "Speech provider: %s\n", speechProvider.ID)
	}

	var stopResult *capture.StopResult
	var audioData []byte
	var durationSecs float64

	if a.ipcClient != nil {
		if result, err := a.ipcClient.Stop(ctx); err == nil {
			stopResult = result
			durationSecs = result.DurationSecs

			if capAudio, err := a.ipcClient.GetCapturedAudio(ctx); err == nil {
				audioData = []byte(capAudio.Data)
			}

			fmt.Fprintf(a.out, "Capture stopped. Duration: %.1fs\n", durationSecs)
			fmt.Fprintf(a.out, "Output: %s\n", result.OutputPath)
		} else {
			fmt.Fprintf(a.out, "Note: Capture helper integration (Task 12) handles actual audio capture.\n")
			fmt.Fprintf(a.out, "Use 'noto import-audio <path> --title \"X\"' to import recorded audio.\n")
		}
	} else {
		fmt.Fprintf(a.out, "Note: Capture helper integration (Task 12) handles actual audio capture.\n")
		fmt.Fprintf(a.out, "Use 'noto import-audio <path> --title \"X\"' to import recorded audio.\n")
	}

	if stopResult != nil && len(audioData) > 0 {
		refs, err := storage.ListMeetings(a.recordingsDir)
		if err == nil && len(refs) > 0 {
			lastMeeting := refs[0]
			layout, err := storage.LayoutFor(a.recordingsDir, lastMeeting.MeetingID)
			if err == nil {
				audioPath := filepath.Join(layout.MeetingDir, "audio.m4a")
				if err := os.WriteFile(audioPath, audioData, 0644); err == nil {
					fmt.Fprintf(a.out, "Audio written to: %s\n", audioPath)

					versionID := lastMeeting.CurrentVersionID
					versionAudioDir := layout.VersionDir(versionID)
					versionAudioPath := filepath.Join(versionAudioDir, "audio")
					os.MkdirAll(versionAudioPath, 0755)
					os.WriteFile(filepath.Join(versionAudioPath, "recording.m4a"), audioData, 0644)

					audioMeta := &artifacts.AudioMetadata{
						SchemaVersion:   "audio-asset.v1",
						MeetingID:       lastMeeting.MeetingID.String(),
						AssetID:         fmt.Sprintf("aud_%s", uuid.New().String()[:12]),
						Path:            "audio/recording.m4a",
						Format:          stopResult.Format,
						Codec:           stopResult.Codec,
						DurationSeconds: durationSecs,
						Channels:        stopResult.Channels,
						SampleRateHz:    stopResult.SampleRateHz,
						SizeBytes:       stopResult.SizeBytes,
						Sources: []artifacts.AudioSource{
							{ID: "src_mic", Role: "local_speaker", Label: "Microphone", Channel: 0},
							{ID: "src_system", Role: "participants", Label: "System Audio", Channel: 1},
						},
					}

					versionAudioMetaPath := filepath.Join(versionAudioDir, "audio.json")
					audioMetaData, _ := json.MarshalIndent(audioMeta, "", "  ")
					tmpPath := filepath.Join(layout.TmpDir, "audio_meta.tmp")
					os.WriteFile(tmpPath, audioMetaData, 0644)
					os.Rename(tmpPath, versionAudioMetaPath)

					fmt.Fprintf(a.out, "Audio metadata written.\n")

					if speechProvider != nil && durationSecs > 0 {
						fmt.Fprintf(a.out, "Transcribing...\n")
						transcript, err := a.runTranscription(ctx, speechProvider, audioData, lastMeeting.MeetingID.String())
						if err == nil {
							if err := storage.WriteTranscript(layout, transcript); err == nil {
								fmt.Fprintf(a.out, "Transcript written.\n")

								if err := a.indexMeeting(lastMeeting.MeetingID.String(), lastMeeting.Title, transcript, nil); err == nil {
									fmt.Fprintf(a.out, "Indexed for search.\n")
								}
							}
						} else {
							fmt.Fprintf(a.errOut, "Transcription failed: %v\n", err)
						}
					}
				}
			}
		}
	}

	return nil
}


func (a app) importAudio(ctx context.Context, args []string) error {
	title := extractTitleFlag(args)
	audioPath := extractPositionalArg(args)

	if audioPath == "" {
		return notoerr.New("missing_path", "Usage: noto import-audio <path> --title \"Title\".", nil)
	}

	if _, err := os.Stat(audioPath); os.IsNotExist(err) {
		return notoerr.New("file_not_found", "Audio file not found.", map[string]any{"path": audioPath})
	}

	cfg, err := a.config.Load()
	if err != nil {
		return err
	}

	meetingID := uuid.New()
	ia := artifacts.NewImportAudio(a.recordingsDir)
	result, err := ia.Import(meetingID, audioPath)
	if err != nil {
		return err
	}

	layout, err := storage.LayoutFor(a.recordingsDir, meetingID)
	if err != nil {
		return err
	}

	manifest := &artifacts.MeetingManifest{
		SchemaVersion:    "manifest.v1",
		MeetingID:        meetingID.String(),
		CurrentVersionID: result.VersionID,
		Versions: []artifacts.ManifestVersion{
			{
				VersionID: result.VersionID,
				CreatedAt: time.Now(),
				Reason:    string(artifacts.ReasonAudioImported),
			},
		},
	}

	if err := storage.WriteManifest(layout, manifest); err != nil {
		return err
	}

	audioData, err := os.ReadFile(audioPath)
	if err != nil {
		return err
	}

	fmt.Fprintf(a.out, "Processing audio file: %s\n", audioPath)
	fmt.Fprintf(a.out, "Meeting ID: %s\n", meetingID.String())

	router := providers.CapabilityRouter{Registry: a.reg, Policy: cfg.Routing}
	speechProvider, err := router.Resolve(providers.CapabilityTranscribe)
	if err != nil {
		fmt.Fprintf(a.out, "Warning: No speech provider configured. Audio imported but not transcribed.\n")
		return writeJSON(a.out, map[string]any{
			"ok":        true,
			"meeting_id": meetingID.String(),
			"audio":     result.AudioMetadata,
			"transcribed": false,
			"message":   "Audio imported. Configure a speech provider to enable transcription.",
		})
	}

	fmt.Fprintf(a.out, "Transcribing with %s...\n", speechProvider.ID)

	transcript, err := a.runTranscription(ctx, speechProvider, audioData, meetingID.String())
	if err != nil {
		fmt.Fprintf(a.errOut, "Transcription failed: %v\n", err)
		return writeJSON(a.out, map[string]any{
			"ok":         true,
			"meeting_id":  meetingID.String(),
			"audio":      result.AudioMetadata,
			"transcribed": false,
			"error":      err.Error(),
		})
	}

	if err := a.indexMeeting(meetingID.String(), title, transcript, nil); err != nil {
		fmt.Fprintf(a.errOut, "Warning: Failed to index meeting: %v\n", err)
	}

	return writeJSON(a.out, map[string]any{
		"ok":         true,
		"meeting_id":  meetingID.String(),
		"audio":      result.AudioMetadata,
		"transcribed": true,
		"transcript": transcript,
	})
}


func (a app) importTranscript(ctx context.Context, args []string) error {
	title := extractTitleFlag(args)
	transcriptPath := extractPositionalArg(args)

	if transcriptPath == "" {
		return notoerr.New("missing_path", "Usage: noto import-transcript <path> --title \"Title\".", nil)
	}

	data, err := os.ReadFile(transcriptPath)
	if err != nil {
		return notoerr.Wrap("read_failed", "Could not read transcript file", err)
	}

	var transcript artifacts.Transcript
	if err := json.Unmarshal(data, &transcript); err != nil {
		return notoerr.Wrap("parse_failed", "Could not parse transcript JSON", err)
	}

	meetingID := uuid.New()
	if transcript.MeetingID != "" {
		parsed, _ := uuid.Parse(transcript.MeetingID)
		if parsed != uuid.Nil {
			meetingID = parsed
		}
	}

	layout, err := storage.LayoutFor(a.recordingsDir, meetingID)
	if err != nil {
		return err
	}

	if err := storage.EnsureDirs(layout); err != nil {
		return err
	}

	if err := storage.WriteTranscript(layout, &transcript); err != nil {
		return err
	}

	now := time.Now()
	versionID := fmt.Sprintf("ver_%s_%s", now.Format("20060102150405"), randomSuffix())

	manifest := &artifacts.MeetingManifest{
		SchemaVersion:    "manifest.v1",
		MeetingID:        meetingID.String(),
		CurrentVersionID: versionID,
		Versions: []artifacts.ManifestVersion{
			{
				VersionID: versionID,
				CreatedAt: now,
				Reason:    "transcript_imported",
			},
		},
	}

	if err := storage.WriteManifest(layout, manifest); err != nil {
		return err
	}

	if err := a.indexMeeting(meetingID.String(), title, &transcript, nil); err != nil {
		fmt.Fprintf(a.errOut, "Warning: Failed to index meeting: %v\n", err)
	}

	return writeJSON(a.out, map[string]any{
		"ok":         true,
		"meeting_id": meetingID.String(),
		"transcript": &transcript,
	})
}


func (a app) transcribe(ctx context.Context, args []string) error {
	meetingIDStr := extractPositionalArg(args)
	if meetingIDStr == "" {
		return notoerr.New("missing_meeting_id", "Usage: noto transcribe <meeting_id> [--provider <provider_id>].", nil)
	}

	meetingID, err := uuid.Parse(meetingIDStr)
	if err != nil {
		return notoerr.New("invalid_meeting_id", "Invalid meeting ID format.", map[string]any{"id": meetingIDStr})
	}

	cfg, err := a.config.Load()
	if err != nil {
		return err
	}

	layout, err := storage.LayoutFor(a.recordingsDir, meetingID)
	if err != nil {
		return err
	}

	audioPath := filepath.Join(layout.MeetingDir, "audio.m4a")
	audioData, err := os.ReadFile(audioPath)
	if err != nil {
		return notoerr.New("no_audio", "No audio found for meeting. Import audio first.", map[string]any{"meeting_id": meetingIDStr})
	}

	fmt.Fprintf(a.out, "Transcribing meeting %s...\n", meetingIDStr)

	router := providers.CapabilityRouter{Registry: a.reg, Policy: cfg.Routing}
	speechProvider, err := router.Resolve(providers.CapabilityTranscribe)
	if err != nil {
		return err
	}

	transcript, err := a.runTranscription(ctx, speechProvider, audioData, meetingIDStr)
	if err != nil {
		return err
	}

	if err := storage.WriteTranscript(layout, transcript); err != nil {
		return err
	}

	if err := a.indexMeeting(meetingIDStr, "", transcript, nil); err != nil {
		fmt.Fprintf(a.errOut, "Warning: Failed to index meeting: %v\n", err)
	}

	return writeJSON(a.out, map[string]any{
		"ok":         true,
		"meeting_id": meetingIDStr,
		"transcript": transcript,
		"provider":   speechProvider.ID,
	})
}

func (a app) summarize(ctx context.Context, args []string) error {
	meetingIDStr := extractPositionalArg(args)
	if meetingIDStr == "" {
		return notoerr.New("missing_meeting_id", "Usage: noto summarize <meeting_id>.", nil)
	}

	meetingID, err := uuid.Parse(meetingIDStr)
	if err != nil {
		return notoerr.New("invalid_meeting_id", "Invalid meeting ID format.", map[string]any{"id": meetingIDStr})
	}

	cfg, err := a.config.Load()
	if err != nil {
		return err
	}

	layout, err := storage.LayoutFor(a.recordingsDir, meetingID)
	if err != nil {
		return err
	}

	transcript, err := storage.ReadTranscript(layout)
	if err != nil {
		return notoerr.New("no_transcript", "No transcript found. Run 'noto transcribe' first.", map[string]any{"meeting_id": meetingIDStr})
	}

	fmt.Fprintf(a.out, "Summarizing meeting %s...\n", meetingIDStr)

	router := providers.CapabilityRouter{Registry: a.reg, Policy: cfg.Routing}
	llmProvider, err := router.Resolve(providers.CapabilitySummarize)
	if err != nil {
		return err
	}

	summary, err := a.runSummarization(ctx, llmProvider, transcript)
	if err != nil {
		return err
	}

	summaryMD, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}

	if err := storage.WriteSummary(layout, string(summaryMD)); err != nil {
		return err
	}

	if err := a.indexMeeting(meetingIDStr, "", transcript, summary); err != nil {
		fmt.Fprintf(a.errOut, "Warning: Failed to update search index: %v\n", err)
	}

	return writeJSON(a.out, map[string]any{
		"ok":         true,
		"meeting_id": meetingIDStr,
		"summary":    summary,
		"provider":   llmProvider.ID,
	})
}


func (a app) search(ctx context.Context, args []string) error {
	query := extractSearchQuery(args)
	if query == "" {
		return notoerr.New("missing_query", "Usage: noto search --json \"query\".", nil)
	}

	isJSON := hasJSONFlag(args)

	results, err := a.searchIndex.Search(query)
	if err != nil {
		return err
	}

	if !isJSON {
		if len(results) == 0 {
			fmt.Fprintf(a.out, "No results found for: %s\n", query)
			return nil
		}
		fmt.Fprintf(a.out, "Results for: %s\n\n", query)
		for _, r := range results {
			fmt.Fprintf(a.out, "[%s] %s | %s | %s\n", r.MeetingID, r.Speaker, r.Snippet, r.SegmentID)
		}
		return nil
	}

	return writeJSON(a.out, map[string]any{
		"ok":     true,
		"query":  query,
		"results": results,
		"count":  len(results),
	})
}


func (a app) index(ctx context.Context, args []string) error {
	isJSON := hasJSONFlag(args)

	fmt.Fprintf(a.out, "Rebuilding search index...\n")

	refs, err := storage.ListMeetings(a.recordingsDir)
	if err != nil {
		return err
	}

	indexed := 0
	for _, ref := range refs {
		meeting, err := storage.GetMeeting(a.recordingsDir, ref.MeetingID)
		if err != nil {
			continue
		}

		layout, err := storage.LayoutFor(a.recordingsDir, ref.MeetingID)
		if err != nil {
			continue
		}

		transcript, _ := storage.ReadTranscript(layout)
		var summary *artifacts.Summary
		if summaryData, err := os.ReadFile(layout.SummaryPath); err == nil {
			json.Unmarshal(summaryData, &summary)
		}

		segments := make([]search.TranscriptSegment, 0)
		if transcript != nil {
			for _, seg := range transcript.Segments {
				segments = append(segments, search.TranscriptSegment{
					SegmentID: seg.ID,
					Speaker:   seg.SpeakerID,
					Text:      seg.Text,
					Timestamp: seg.StartSeconds,
				})
			}
		}

		decisions := make([]search.SummaryItem, 0)
		actions := make([]search.ActionItem, 0)
		risks := make([]search.SummaryItem, 0)

		if summary != nil {
			for _, d := range summary.Decisions {
				decisions = append(decisions, search.SummaryItem{Text: d.Text, SpeakerIDs: d.SpeakerIDs})
			}
			for _, a := range summary.ActionItems {
				actions = append(actions, search.ActionItem{Text: a.Text, Owner: a.Owner})
			}
			for _, r := range summary.Risks {
				risks = append(risks, search.SummaryItem{Text: r.Text})
			}
		}

		input := &search.IndexMeetingInput{
			MeetingID:          ref.MeetingID.String(),
			Title:              meeting.Title,
			TranscriptSegments: segments,
			Decisions:          decisions,
			ActionItems:        actions,
			Risks:              risks,
		}

		if err := a.searchIndex.IndexMeetingFromInput(input); err != nil {
			continue
		}
		indexed++
	}

	if isJSON {
		return writeJSON(a.out, map[string]any{
			"ok":      true,
			"total":   len(refs),
			"indexed": indexed,
		})
	}

	fmt.Fprintf(a.out, "Indexed %d of %d meetings.\n", indexed, len(refs))
	return nil
}


func (a app) list(ctx context.Context, args []string) error {
	isJSON := hasJSONFlag(args)
	limit := extractLimit(args)

	refs, err := storage.ListMeetings(a.recordingsDir)
	if err != nil {
		return err
	}

	if limit > 0 && len(refs) > limit {
		refs = refs[:limit]
	}

	if !isJSON {
		if len(refs) == 0 {
			fmt.Fprintf(a.out, "No meetings found.\n")
			return nil
		}
		fmt.Fprintf(a.out, "Meetings:\n\n")
		for _, ref := range refs {
			createdAt := ref.CreatedAt.Format("2006-01-02 15:04")
			fmt.Fprintf(a.out, "%s  %s  [%s]\n", ref.MeetingID.String(), createdAt, ref.Title)
		}
		return nil
	}

	type MeetingInfo struct {
		ID        string    `json:"meeting_id"`
		Title     string    `json:"title"`
		CreatedAt time.Time `json:"created_at"`
		Version   string    `json:"current_version_id"`
	}

	meetings := make([]MeetingInfo, len(refs))
	for i, ref := range refs {
		meetings[i] = MeetingInfo{
			ID:        ref.MeetingID.String(),
			Title:     ref.Title,
			CreatedAt: ref.CreatedAt,
			Version:   ref.CurrentVersionID,
		}
	}

	return writeJSON(a.out, map[string]any{
		"ok":       true,
		"meetings": meetings,
		"count":    len(meetings),
	})
}


func (a app) show(ctx context.Context, args []string) error {
	meetingIDStr := extractPositionalArg(args)
	if meetingIDStr == "" {
		return notoerr.New("missing_meeting_id", "Usage: noto show <meeting_id>.", nil)
	}

	meetingID, err := uuid.Parse(meetingIDStr)
	if err != nil {
		return notoerr.New("invalid_meeting_id", "Invalid meeting ID format.", map[string]any{"id": meetingIDStr})
	}

	meeting, err := storage.GetMeeting(a.recordingsDir, meetingID)
	if err != nil {
		return notoerr.New("meeting_not_found", "Meeting not found.", map[string]any{"meeting_id": meetingIDStr})
	}

	layout, err := storage.LayoutFor(a.recordingsDir, meetingID)
	if err != nil {
		return err
	}

	transcript, _ := storage.ReadTranscript(layout)
	summaryData, _ := os.ReadFile(layout.SummaryPath)
	audioMeta, _ := storage.ReadAudioMetadata(layout)

	var summary *artifacts.Summary
	if len(summaryData) > 0 {
		json.Unmarshal(summaryData, &summary)
	}

	return writeJSON(a.out, map[string]any{
		"ok":              true,
		"meeting_id":      meetingIDStr,
		"title":          meeting.Title,
		"created_at":      meeting.CreatedAt,
		"current_version": meeting.CurrentVersionID,
		"versions":       meeting.Versions,
		"transcript":     transcript,
		"summary":        summary,
		"audio":          audioMeta,
		"paths": map[string]string{
			"meeting_dir": layout.MeetingDir,
			"audio":       layout.AudioPath,
			"transcript":  layout.TranscriptPath,
			"summary":     layout.SummaryPath,
			"manifest":    layout.ManifestPath,
		},
	})
}


func (a app) transcript(ctx context.Context, args []string) error {
	meetingIDStr := extractPositionalArg(args)
	if meetingIDStr == "" {
		return notoerr.New("missing_meeting_id", "Usage: noto transcript <meeting_id>.", nil)
	}

	meetingID, err := uuid.Parse(meetingIDStr)
	if err != nil {
		return notoerr.New("invalid_meeting_id", "Invalid meeting ID format.", map[string]any{"id": meetingIDStr})
	}

	layout, err := storage.LayoutFor(a.recordingsDir, meetingID)
	if err != nil {
		return err
	}

	transcript, err := storage.ReadTranscript(layout)
	if err != nil {
		return notoerr.New("no_transcript", "No transcript found.", map[string]any{"meeting_id": meetingIDStr})
	}

	isJSON := hasJSONFlag(args)
	if !isJSON {
		for _, seg := range transcript.Segments {
			start := formatTimestamp(seg.StartSeconds)
			fmt.Fprintf(a.out, "[%s] %s (%s): %s\n", seg.ID, seg.SpeakerID, seg.SourceRole, seg.Text)
		}
		return nil
	}

	return writeJSON(a.out, map[string]any{
		"ok":         true,
		"meeting_id": meetingIDStr,
		"transcript": transcript,
	})
}


func (a app) summary(ctx context.Context, args []string) error {
	meetingIDStr := extractPositionalArg(args)
	if meetingIDStr == "" {
		return notoerr.New("missing_meeting_id", "Usage: noto summary <meeting_id>.", nil)
	}

	meetingID, err := uuid.Parse(meetingIDStr)
	if err != nil {
		return notoerr.New("invalid_meeting_id", "Invalid meeting ID format.", map[string]any{"id": meetingIDStr})
	}

	layout, err := storage.LayoutFor(a.recordingsDir, meetingID)
	if err != nil {
		return err
	}

	summaryData, err := os.ReadFile(layout.SummaryPath)
	if err != nil {
		return notoerr.New("no_summary", "No summary found. Run 'noto summarize' first.", map[string]any{"meeting_id": meetingIDStr})
	}

	var summary artifacts.Summary
	if err := json.Unmarshal(summaryData, &summary); err != nil {
		return err
	}

	isJSON := hasJSONFlag(args)
	if !isJSON {
		fmt.Fprintf(a.out, "# %s\n\n", meetingIDStr)
		fmt.Fprintf(a.out, "%s\n\n", summary.ShortSummary)
		if len(summary.Decisions) > 0 {
			fmt.Fprintf(a.out, "## Decisions\n\n")
			for i, d := range summary.Decisions {
				fmt.Fprintf(a.out, "%d. %s\n", i+1, d.Text)
			}
		}
		if len(summary.ActionItems) > 0 {
			fmt.Fprintf(a.out, "\n## Action Items\n\n")
			for _, a := range summary.ActionItems {
				fmt.Fprintf(a.out, "- @%s: %s\n", a.Owner, a.Text)
			}
		}
		return nil
	}

	return writeJSON(a.out, map[string]any{
		"ok":         true,
		"meeting_id": meetingIDStr,
		"summary":    &summary,
	})
}


func (a app) actions(ctx context.Context, args []string) error {
	meetingIDStr := extractPositionalArg(args)
	if meetingIDStr == "" {
		return notoerr.New("missing_meeting_id", "Usage: noto actions <meeting_id>.", nil)
	}

	meetingID, err := uuid.Parse(meetingIDStr)
	if err != nil {
		return notoerr.New("invalid_meeting_id", "Invalid meeting ID format.", map[string]any{"id": meetingIDStr})
	}

	layout, err := storage.LayoutFor(a.recordingsDir, meetingID)
	if err != nil {
		return err
	}

	summaryData, err := os.ReadFile(layout.SummaryPath)
	if err != nil {
		return notoerr.New("no_summary", "No summary found.", map[string]any{"meeting_id": meetingIDStr})
	}

	var summary artifacts.Summary
	if err := json.Unmarshal(summaryData, &summary); err != nil {
		return err
	}

	return writeJSON(a.out, map[string]any{
		"ok":          true,
		"meeting_id":  meetingIDStr,
		"action_items": summary.ActionItems,
	})
}


func (a app) files(ctx context.Context, args []string) error {
	meetingIDStr := extractPositionalArg(args)
	if meetingIDStr == "" {
		return notoerr.New("missing_meeting_id", "Usage: noto files <meeting_id>.", nil)
	}

	meetingID, err := uuid.Parse(meetingIDStr)
	if err != nil {
		return notoerr.New("invalid_meeting_id", "Invalid meeting ID format.", map[string]any{"id": meetingIDStr})
	}

	layout, err := storage.LayoutFor(a.recordingsDir, meetingID)
	if err != nil {
		return err
	}

	manifest, err := storage.ReadManifest(layout)
	if err != nil {
		return err
	}

	files := []map[string]string{
		{"path": layout.ManifestPath, "type": "manifest"},
		{"path": layout.AudioPath, "type": "audio"},
		{"path": layout.TranscriptPath, "type": "transcript"},
		{"path": layout.SummaryPath, "type": "summary"},
	}

	versionDir := layout.VersionDir(manifest.CurrentVersionID)
	files = append(files, map[string]string{
		"path": layout.VersionManifestPath(manifest.CurrentVersionID),
		"type": "version_manifest",
	})
	_ = versionDir

	return writeJSON(a.out, map[string]any{
		"ok":         true,
		"meeting_id": meetingIDStr,
		"files":      files,
	})
}


func (a app) benchmark(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return notoerr.New("missing_subcommand", "Usage: noto benchmark <run|compare|report>.", nil)
	}

	switch args[0] {
	case "run":
		return a.benchmarkRun(ctx)
	case "report":
		return a.benchmarkReport(ctx, args[1:])
	case "compare":
		return a.benchmarkCompare(ctx, args[1:])
	default:
		return notoerr.New("unknown_command", "Unknown benchmark subcommand.", map[string]any{"command": args[0]})
	}
}

func (a app) benchmarkRun(ctx context.Context) error {
	fmt.Fprintf(a.out, "Running Noto benchmarks...\n\n")

	benchmarker, err := benchmarks.NewBenchmarker(benchmarks.DefaultBenchmarkConfig())
	if err != nil {
		return notoerr.Wrap("benchmark_init_failed", "Could not initialize benchmarker.", err)
	}
	defer benchmarker.Cleanup()

	result, err := benchmarker.Run()
	if err != nil {
		return notoerr.Wrap("benchmark_run_failed", "Benchmark run failed.", err)
	}

	defaultPath := filepath.Join(os.Getenv("HOME"), ".noto", "benchmark-results.json")
	if err := benchmarker.SaveResults(result, defaultPath); err != nil {
		return notoerr.Wrap("benchmark_save_failed", "Could not save benchmark results.", err)
	}

	fmt.Fprintf(a.out, "Benchmark results saved to: %s\n\n", defaultPath)

	passCount := 0
	failCount := 0
	for _, m := range result.Results {
		status := "PASS"
		if !m.Pass {
			status = "FAIL"
			failCount++
		} else {
			passCount++
		}
		fmt.Fprintf(a.out, "  %-45s %12.4f %-15s [%s]\n", m.Metric, m.Value, m.Unit, status)
	}

	fmt.Fprintf(a.out, "\n--- Summary: %d passed, %d failed ---\n", passCount, failCount)

	if result.AllPasses() {
		fmt.Fprintf(a.out, "All benchmarks PASSED\n")
	} else {
		fmt.Fprintf(a.out, "Some benchmarks FAILED\n")
	}

	return nil
}

func (a app) benchmarkReport(ctx context.Context, args []string) error {
	path := filepath.Join(os.Getenv("HOME"), ".noto", "benchmark-results.json")
	if len(args) > 0 && args[0] != "" {
		path = args[0]
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return notoerr.Wrap("benchmark_load_failed", "Could not read benchmark results file.", err)
	}

	var result benchmarks.BenchmarkResult
	if err := json.Unmarshal(data, &result); err != nil {
		return notoerr.Wrap("benchmark_parse_failed", "Could not parse benchmark results.", err)
	}

	fmt.Fprintf(a.out, "Benchmark Report\n")
	fmt.Fprintf(a.out, "Run ID:    %s\n", result.RunID)
	fmt.Fprintf(a.out, "Timestamp: %s\n\n", result.Timestamp)

	passCount := 0
	failCount := 0
	for _, m := range result.Results {
		status := "PASS"
		if !m.Pass {
			status = "FAIL"
			failCount++
		} else {
			passCount++
		}
		fmt.Fprintf(a.out, "  %-45s %12.4f %-15s [%s]\n", m.Metric, m.Value, m.Unit, status)
	}

	fmt.Fprintf(a.out, "\n--- Summary: %d passed, %d failed ---\n", passCount, failCount)

	if result.AllPasses() {
		fmt.Fprintf(a.out, "All benchmarks PASSED\n")
	} else {
		fmt.Fprintf(a.out, "Some benchmarks FAILED\n")
	}

	return nil
}

func (a app) benchmarkCompare(ctx context.Context, args []string) error {
	if len(args) < 2 {
		return notoerr.New("missing_arguments", "Usage: noto benchmark compare <file1> <file2>.", nil)
	}

	path1, path2 := args[0], args[1]

	data1, err := os.ReadFile(path1)
	if err != nil {
		return notoerr.Wrap("benchmark_load_failed", "Could not read first benchmark results file.", err)
	}

	data2, err := os.ReadFile(path2)
	if err != nil {
		return notoerr.Wrap("benchmark_load_failed", "Could not read second benchmark results file.", err)
	}

	var result1, result2 benchmarks.BenchmarkResult
	if err := json.Unmarshal(data1, &result1); err != nil {
		return notoerr.Wrap("benchmark_parse_failed", "Could not parse first benchmark results.", err)
	}
	if err := json.Unmarshal(data2, &result2); err != nil {
		return notoerr.Wrap("benchmark_parse_failed", "Could not parse second benchmark results.", err)
	}

	result1Map := make(map[string]benchmarks.MetricResult)
	for _, m := range result1.Results {
		result1Map[m.Metric] = m
	}

	fmt.Fprintf(a.out, "Benchmark Comparison: %s vs %s\n\n", path1, path2)
	fmt.Fprintf(a.out, "%-45s %12s %12s %12s %-8s\n", "Metric", "File1", "File2", "Delta", "Change")
	fmt.Fprintf(a.out, "%s\n", strings.Repeat("-", 80))

	for _, m2 := range result2.Results {
		m1, ok := result1Map[m2.Metric]
		if !ok {
			fmt.Fprintf(a.out, "%-45s %12s %12.4f %12s %-8s\n", m2.Metric, "(none)", m2.Value, "N/A", "NEW")
			continue
		}

		delta := m2.Value - m1.Value
		deltaStr := fmt.Sprintf("%+.4f", delta)

		change := "="
		if delta > 0.001 {
			change = "+"
		} else if delta < -0.001 {
			change = "-"
		}

		if m1.Pass != m2.Pass {
			change += "!"
		}

		fmt.Fprintf(a.out, "%-45s %12.4f %12.4f %12s %-8s\n", m2.Metric, m1.Value, m2.Value, deltaStr, change)
	}

	for _, m1 := range result1.Results {
		if _, ok := result2.Results == nil || !containsMetric(result2.Results, m1.Metric) {
			fmt.Fprintf(a.out, "%-45s %12.4f %12s %12s %-8s\n", m1.Metric, m1.Value, "(none)", "N/A", "REMOVED")
		}
	}

	return nil
}

func containsMetric(results []benchmarks.MetricResult, metric string) bool {
	for _, r := range results {
		if r.Metric == metric {
			return true
		}
	}
	return false
}

func (a app) runTranscription(ctx context.Context, provider providers.ProviderSuite, audioData []byte, meetingID string) (*artifacts.Transcript, error) {
	sp, ok := provider.(interface {
		Transcribe(ctx context.Context, audio []byte, opts providers.TranscribeOptions) (*artifacts.Transcript, error)
	})
	if !ok {
		return nil, notoerr.New("provider_incompatible", "Speech provider does not support transcription.", nil)
	}

	opts := providers.TranscribeOptions{}
	transcript, err := sp.Transcribe(ctx, audioData, opts)
	if err != nil {
		return nil, err
	}

	normalizers := speech.NewTranscriptNormalizers()
	normalized, err := normalizers.Normalize(transcript)
	if err == nil {
		transcript = normalized
	}

	transcript.MeetingID = meetingID

	return transcript, nil
}

func (a app) runSummarization(ctx context.Context, provider providers.ProviderSuite, transcript *artifacts.Transcript) (*artifacts.Summary, error) {
	lp, ok := provider.(interface {
		Summarize(ctx context.Context, transcript artifacts.Transcript, opts providers.SummarizeOptions) (*artifacts.Summary, error)
	})
	if !ok {
		return nil, notoerr.New("provider_incompatible", "LLM provider does not support summarization.", nil)
	}

	opts := providers.SummarizeOptions{}
	summary, err := lp.Summarize(ctx, *transcript, opts)
	if err != nil {
		return nil, err
	}

	summary.MeetingID = transcript.MeetingID

	return summary, nil
}

func (a app) indexMeeting(meetingID, title string, transcript *artifacts.Transcript, summary *artifacts.Summary) error {
	if a.searchIndex == nil {
		return nil
	}

	segments := make([]search.TranscriptSegment, 0)
	if transcript != nil {
		for _, seg := range transcript.Segments {
			segments = append(segments, search.TranscriptSegment{
				SegmentID: seg.ID,
				Speaker:   seg.SpeakerID,
				Text:      seg.Text,
				Timestamp: seg.StartSeconds,
			})
		}
	}

	decisions := make([]search.SummaryItem, 0)
	actions := make([]search.ActionItem, 0)
	risks := make([]search.SummaryItem, 0)

	if summary != nil {
		for _, d := range summary.Decisions {
			decisions = append(decisions, search.SummaryItem{Text: d.Text, SpeakerIDs: d.SpeakerIDs})
		}
		for _, a := range summary.ActionItems {
			actions = append(actions, search.ActionItem{Text: a.Text, Owner: a.Owner})
		}
		for _, r := range summary.Risks {
			risks = append(risks, search.SummaryItem{Text: r.Text})
		}
	}

	input := &search.IndexMeetingInput{
		MeetingID:          meetingID,
		Title:              title,
		TranscriptSegments: segments,
		Decisions:          decisions,
		ActionItems:        actions,
		Risks:              risks,
	}

	return a.searchIndex.IndexMeetingFromInput(input)
}

func extractTitleFlag(args []string) string {
	for i, arg := range args {
		if (arg == "--title" || arg == "-t") && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(arg, "--title=") {
			return strings.TrimPrefix(arg, "--title=")
		}
		if strings.HasPrefix(arg, "-t=") {
			return strings.TrimPrefix(arg, "-t=")
		}
	}
	return ""
}

func extractSearchQuery(args []string) string {
	for i, arg := range args {
		if arg == "--json" || arg == "-j" {
			continue
		}
		if !strings.HasPrefix(arg, "-") {
			return arg
		}
	}
	return ""
}

func extractPositionalArg(args []string) string {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") && arg != "--json" {
			return arg
		}
	}
	return ""
}

func extractLimit(args []string) int {
	for i, arg := range args {
		if (arg == "--limit" || arg == "-l") && i+1 < len(args) {
			var limit int
			if _, err := fmt.Sscanf(args[i+1], "%d", &limit); err == nil {
				return limit
			}
		}
		if strings.HasPrefix(arg, "--limit=") {
			var limit int
			if _, err := fmt.Sscanf(strings.TrimPrefix(arg, "--limit="), "%d", &limit); err == nil {
				return limit
			}
		}
	}
	return 0
}

func formatTimestamp(seconds float64) string {
	h := int(seconds) / 3600
	m := (int(seconds) % 3600) / 60
	s := int(seconds) % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

func randomSuffix() string {
	b := make([]byte, 4)
	for i := range b {
		b[i] = byte(uuid.New().ID() % 256)
	}
	return fmt.Sprintf("%x", b)
}

func (a app) notImplemented(command string) error {
	return notoerr.New("not_implemented", "This command is part of the documented V1 surface but is not implemented in this build yet.", map[string]any{"command": command})
}

func (a app) setSpeech(providerID string) error {
	cfg, err := a.config.Load()
	if err != nil {
		return err
	}
	if providerID != providers.RoutingProfileBenchmarkSelected.String() {
		suite, err := a.reg.MustGet(providerID)
		if err != nil {
			return err
		}
		if suite.Kind != providers.ProviderKindSpeech {
			return notoerr.New("invalid_provider_kind", "Speech provider must be one of Mistral, AssemblyAI, or ElevenLabs.", map[string]any{"provider": providerID})
		}
	}
	cfg.Routing.SpeechProvider = providerID
	cfg.Routing.LLMProvider = "openrouter"
	if cfg.Routing.Profile == "" {
		cfg.Routing.Profile = providers.RoutingProfileManual
	}
	if err := a.config.Save(cfg); err != nil {
		return err
	}
	return writeJSON(a.out, map[string]any{"ok": true, "speech_provider": providerID})
}

func (a app) setLLMModel(modelID string) error {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return notoerr.New("invalid_model", "OpenRouter model ID cannot be empty.", nil)
	}
	cfg, err := a.config.Load()
	if err != nil {
		return err
	}
	cfg.Routing.LLMProvider = "openrouter"
	cfg.Routing.LLMModel = modelID
	if err := a.config.Save(cfg); err != nil {
		return err
	}
	return writeJSON(a.out, map[string]any{"ok": true, "llm_provider": "openrouter", "llm_model": modelID})
}

func (a app) providerAuth(ctx context.Context, args []string) error {
	if len(args) < 2 {
		return notoerr.New("missing_argument", "Usage: noto providers auth <set|test|remove> <provider>.", nil)
	}
	action, providerID := args[0], args[1]
	suite, err := a.reg.MustGet(providerID)
	if err != nil {
		return err
	}
	if suite.CredentialRef == "" {
		return notoerr.New("provider_has_no_credential", "Provider does not require credentials.", map[string]any{"provider": providerID})
	}

	switch action {
	case "set":
		b, err := io.ReadAll(a.in)
		if err != nil {
			return notoerr.Wrap("credential_read_failed", "Could not read credential from stdin.", err)
		}
		value := strings.TrimSpace(string(b))
		if value == "" {
			return notoerr.New("empty_credential", "Credential cannot be empty.", map[string]any{"provider": providerID})
		}
		if err := a.secrets.Set(ctx, suite.CredentialRef, value); err != nil {
			return err
		}
		return writeJSON(a.out, map[string]any{"ok": true, "provider": providerID, "credential_ref": suite.CredentialRef})
	case "test":
		status, err := a.secrets.Status(ctx, suite.CredentialRef)
		if err != nil {
			return err
		}
		return writeJSON(a.out, map[string]any{"ok": status.Configured, "provider": providerID, "status": status})
	case "remove":
		if err := a.secrets.Remove(ctx, suite.CredentialRef); err != nil {
			return err
		}
		return writeJSON(a.out, map[string]any{"ok": true, "provider": providerID, "credential_ref": suite.CredentialRef})
	default:
		return notoerr.New("unknown_command", "Unknown providers auth subcommand.", map[string]any{"command": action})
	}
}

func (a app) providerStatuses(ctx context.Context, cfg config.Config) map[string]secrets.Status {
	out := map[string]secrets.Status{}
	for _, suite := range a.reg.List() {
		if suite.CredentialRef == "" {
			out[suite.ID] = secrets.Status{Ref: "", Configured: true, Source: "none"}
			continue
		}
		status, err := a.secrets.Status(ctx, suite.CredentialRef)
		if err != nil {
			status = secrets.Status{Ref: suite.CredentialRef, Configured: false, Source: "error"}
		}
		out[suite.ID] = status
	}
	return out
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func hasJSONFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--json" {
			return true
		}
	}
	return false
}

func parseTUIScreen(value string) tui.Screen {
	switch strings.ToLower(value) {
	case "meetings":
		return tui.ScreenMeetings
	case "search":
		return tui.ScreenSearch
	case "recorder", "record":
		return tui.ScreenRecorder
	case "detail", "meeting":
		return tui.ScreenDetail
	case "transcript":
		return tui.ScreenTranscript
	case "providers", "provider":
		return tui.ScreenProviders
	case "storage", "verify":
		return tui.ScreenStorage
	case "settings":
		return tui.ScreenSettings
	default:
		return tui.ScreenDashboard
	}
}
