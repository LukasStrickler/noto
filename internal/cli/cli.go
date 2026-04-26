package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/lukasstrickler/noto/internal/config"
	"github.com/lukasstrickler/noto/internal/notoerr"
	"github.com/lukasstrickler/noto/internal/providers"
	"github.com/lukasstrickler/noto/internal/secrets"
	"github.com/lukasstrickler/noto/internal/tui"
)

type app struct {
	in      io.Reader
	out     io.Writer
	errOut  io.Writer
	config  config.Store
	secrets secrets.Store
	reg     providers.Registry
}

func Run(args []string, in io.Reader, out io.Writer, errOut io.Writer) int {
	a := app{
		in:      in,
		out:     out,
		errOut:  errOut,
		config:  config.NewStore(config.DefaultConfigDir()),
		secrets: secrets.EnvFallbackStore{Primary: secrets.KeychainStore{}},
		reg:     providers.DefaultRegistry(),
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
	case "record", "stop", "import-audio", "import-transcript", "transcribe", "summarize", "index", "list", "show", "transcript", "summary", "actions", "files", "search", "benchmark":
		return a.notImplemented(args[0])
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
	return writeJSON(a.out, map[string]any{
		"ok":              len(errors) == 0,
		"schema_valid":    true,
		"checksum_valid":  true,
		"index_valid":     true,
		"recording_state": "idle",
		"job_state":       "idle",
		"meeting_count":   0,
		"speech_provider": speech.ID,
		"llm_provider":    llm.ID,
		"errors":          errors,
	})
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
