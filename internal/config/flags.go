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

// BindFlags binds configuration keys to CLI flags using Viper.
func BindFlags(flagSet *pflag.FlagSet, v viperCfg) {
	flagSet.String(FlagRecordingsDir, "", FlagDescriptions[FlagRecordingsDir])
	flagSet.String(FlagArtifactRoot, "", FlagDescriptions[FlagArtifactRoot])
	flagSet.String(FlagConfigDir, "", FlagDescriptions[FlagConfigDir])
	flagSet.String(FlagSTTProvider, "", FlagDescriptions[FlagSTTProvider])
	flagSet.String(FlagLLMProvider, "", FlagDescriptions[FlagLLMProvider])
	flagSet.String(FlagLLMModel, "", FlagDescriptions[FlagLLMModel])
	flagSet.String(FlagSummarizer, "", FlagDescriptions[FlagSummarizer])
	flagSet.String(FlagUITheme, "", FlagDescriptions[FlagUITheme])
	flagSet.Bool(FlagSyncEnabled, false, FlagDescriptions[FlagSyncEnabled])
	flagSet.String(FlagSyncEndpoint, "", FlagDescriptions[FlagSyncEndpoint])
	flagSet.String(FlagSyncBucket, "", FlagDescriptions[FlagSyncBucket])
	flagSet.String(FlagStorageType, "", FlagDescriptions[FlagStorageType])
	flagSet.String(FlagStorageLocalPath, "", FlagDescriptions[FlagStorageLocalPath])
	flagSet.String(FlagStorageS3Bucket, "", FlagDescriptions[FlagStorageS3Bucket])
	flagSet.String(FlagStorageS3Region, "", FlagDescriptions[FlagStorageS3Region])
	flagSet.String(FlagStorageS3Endpoint, "", FlagDescriptions[FlagStorageS3Endpoint])

	_ = v.BindPFlag(KeyRecordingsDir, flagSet.Lookup(FlagRecordingsDir))
	_ = v.BindPFlag(KeyArtifactRoot, flagSet.Lookup(FlagArtifactRoot))
	_ = v.BindPFlag(KeyConfigDir, flagSet.Lookup(FlagConfigDir))
	_ = v.BindPFlag(KeySTTDefault, flagSet.Lookup(FlagSTTProvider))
	_ = v.BindPFlag(KeyLLMDefault, flagSet.Lookup(FlagLLMProvider))
	_ = v.BindPFlag(KeyRoutingLLMModel, flagSet.Lookup(FlagLLMModel))
	_ = v.BindPFlag(KeySummarizer, flagSet.Lookup(FlagSummarizer))
	_ = v.BindPFlag(KeyUITheme, flagSet.Lookup(FlagUITheme))
	_ = v.BindPFlag(KeySyncEnabled, flagSet.Lookup(FlagSyncEnabled))
	_ = v.BindPFlag(KeySyncEndpoint, flagSet.Lookup(FlagSyncEndpoint))
	_ = v.BindPFlag(KeySyncBucket, flagSet.Lookup(FlagSyncBucket))
	_ = v.BindPFlag(KeyStorageType, flagSet.Lookup(FlagStorageType))
	_ = v.BindPFlag(KeyStorageLocalPath, flagSet.Lookup(FlagStorageLocalPath))
	_ = v.BindPFlag(KeyStorageS3Bucket, flagSet.Lookup(FlagStorageS3Bucket))
	_ = v.BindPFlag(KeyStorageS3Region, flagSet.Lookup(FlagStorageS3Region))
	_ = v.BindPFlag(KeyStorageS3Endpoint, flagSet.Lookup(FlagStorageS3Endpoint))
}

// ViperConfig is the interface viper implements.
type viperCfg interface {
	BindPFlag(key string, flag *pflag.Flag) error
}

// NewFlagSet creates a new flag set with all noto CLI flags bound.
func NewFlagSet() *pflag.FlagSet {
	fs := pflag.NewFlagSet("noto", pflag.ContinueOnError)
	fs.SetInterspersed(false)
	return fs
}
