package config

import (
	"github.com/spf13/pflag"
)

// Flag names and descriptions for CLI flag definitions.
const (
	FlagRecordingsDir   = "recordings-dir"
	FlagArtifactRoot    = "artifact-root"
	FlagConfigDir       = "config-dir"
	FlagSTTProvider     = "provider-stt"
	FlagLLMProvider     = "provider-llm"
	FlagLLMModel        = "model-llm"
	FlagSummarizer      = "provider-summarizer"
	FlagUITheme         = "ui-theme"
	FlagSyncEnabled     = "sync-enabled"
	FlagSyncEndpoint    = "sync-endpoint"
	FlagSyncBucket      = "sync-bucket"
	FlagStorageType     = "storage-type"
	FlagStorageLocalPath = "storage-local-path"
	FlagStorageS3Bucket = "storage-s3-bucket"
	FlagStorageS3Region = "storage-s3-region"
	FlagStorageS3Endpoint = "storage-s3-endpoint"
)

// FlagDescriptions holds descriptions for each CLI flag.
var FlagDescriptions = map[string]string{
	FlagRecordingsDir:    "Directory for meeting recordings",
	FlagArtifactRoot:     "Root directory for Noto artifacts",
	FlagConfigDir:        "Configuration directory",
	FlagSTTProvider:      "Default speech-to-text provider",
	FlagLLMProvider:      "Default LLM provider",
	FlagLLMModel:         "Default LLM model",
	FlagSummarizer:       "Default summarization provider",
	FlagUITheme:          "UI theme (dark/light)",
	FlagSyncEnabled:      "Enable cloud sync",
	FlagSyncEndpoint:     "Sync gateway endpoint URL",
	FlagSyncBucket:       "Sync bucket name",
	FlagStorageType:       "Storage backend type (local/s3)",
	FlagStorageLocalPath:  "Local storage path",
	FlagStorageS3Bucket:   "S3 bucket name",
	FlagStorageS3Region:  "S3 region",
	FlagStorageS3Endpoint: "S3 endpoint URL (for R2)",
}

// BindFlags binds configuration keys to CLI flags using Viper. Idempotent
// per (FlagSet, name): if the caller already registered a flag by the
// same name we leave it alone so binding still works without a panic.
func BindFlags(flagSet *pflag.FlagSet, v viperCfg) {
	addStr := func(name string) {
		if flagSet.Lookup(name) == nil {
			flagSet.String(name, "", FlagDescriptions[name])
		}
	}
	addBool := func(name string) {
		if flagSet.Lookup(name) == nil {
			flagSet.Bool(name, false, FlagDescriptions[name])
		}
	}
	addStr(FlagRecordingsDir)
	addStr(FlagArtifactRoot)
	addStr(FlagConfigDir)
	addStr(FlagSTTProvider)
	addStr(FlagLLMProvider)
	addStr(FlagLLMModel)
	addStr(FlagSummarizer)
	addStr(FlagUITheme)
	addBool(FlagSyncEnabled)
	addStr(FlagSyncEndpoint)
	addStr(FlagSyncBucket)
	addStr(FlagStorageType)
	addStr(FlagStorageLocalPath)
	addStr(FlagStorageS3Bucket)
	addStr(FlagStorageS3Region)
	addStr(FlagStorageS3Endpoint)

	bind := func(key, flagName string) {
		f := flagSet.Lookup(flagName)
		_ = v.BindPFlag(key, f)
		// viper.BindPFlag only "wins" when the flag is .Changed. To make
		// a non-empty default value also override the config-default we
		// surface the flag default value as a viper default — config
		// files and env vars still take precedence, which is what
		// callers expect.
		if f != nil {
			if d := f.DefValue; d != "" && d != "false" {
				v.SetDefault(key, d)
			}
		}
	}
	bind(KeyRecordingsDir, FlagRecordingsDir)
	bind(KeyArtifactRoot, FlagArtifactRoot)
	bind(KeyConfigDir, FlagConfigDir)
	bind(KeySTTDefault, FlagSTTProvider)
	bind(KeyLLMDefault, FlagLLMProvider)
	bind(KeyRoutingLLMModel, FlagLLMModel)
	bind(KeySummarizer, FlagSummarizer)
	bind(KeyUITheme, FlagUITheme)
	bind(KeySyncEnabled, FlagSyncEnabled)
	bind(KeySyncEndpoint, FlagSyncEndpoint)
	bind(KeySyncBucket, FlagSyncBucket)
	bind(KeyStorageType, FlagStorageType)
	bind(KeyStorageLocalPath, FlagStorageLocalPath)
	bind(KeyStorageS3Bucket, FlagStorageS3Bucket)
	bind(KeyStorageS3Region, FlagStorageS3Region)
	bind(KeyStorageS3Endpoint, FlagStorageS3Endpoint)
}

// ViperConfig is the interface viper implements. Only the subset of
// methods BindFlags needs.
type viperCfg interface {
	BindPFlag(key string, flag *pflag.Flag) error
	SetDefault(key string, value any)
}

// NewFlagSet creates a new flag set with all noto CLI flags bound.
func NewFlagSet() *pflag.FlagSet {
	fs := pflag.NewFlagSet("noto", pflag.ContinueOnError)
	fs.SetInterspersed(false)
	return fs
}
