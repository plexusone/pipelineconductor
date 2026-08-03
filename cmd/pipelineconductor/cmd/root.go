// Package cmd provides the CLI commands for PipelineConductor.
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cfgFile string

// rootCmd represents the base command.
var rootCmd = &cobra.Command{
	Use:   "pipelineconductor",
	Short: "Multi-repo CI/CD pipeline orchestration and compliance",
	Long: `PipelineConductor is a tool for managing CI/CD pipeline consistency
across hundreds of repositories. It scans repositories, evaluates them
against Cedar policies, and can automatically remediate violations via PRs.

Features:
  - Multi-org repository scanning
  - Policy-as-code with Cedar
  - Compliance reporting (JSON, SARIF, Markdown)
  - Automated remediation via pull requests`,
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.pipelineconductor.yaml)")
	rootCmd.PersistentFlags().String("github-token", "", "GitHub token (or set GITHUB_TOKEN env var)")
	rootCmd.PersistentFlags().StringSlice("orgs", nil, "GitHub organizations to scan")
	rootCmd.PersistentFlags().String("policy-repo", "", "Policy repository (e.g., owner/repo@ref)")
	rootCmd.PersistentFlags().String("profile", "default", "Profile to use for evaluation")
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Verbose output")

	_ = viper.BindPFlag("github_token", rootCmd.PersistentFlags().Lookup("github-token"))
	_ = viper.BindPFlag("orgs", rootCmd.PersistentFlags().Lookup("orgs"))
	_ = viper.BindPFlag("policy_repo", rootCmd.PersistentFlags().Lookup("policy-repo"))
	_ = viper.BindPFlag("profile", rootCmd.PersistentFlags().Lookup("profile"))
	_ = viper.BindPFlag("verbose", rootCmd.PersistentFlags().Lookup("verbose"))
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		home, err := os.UserHomeDir()
		cobra.CheckErr(err)

		viper.AddConfigPath(home)
		viper.AddConfigPath(".")
		viper.SetConfigType("yaml")
		viper.SetConfigName(".pipelineconductor")
	}

	viper.SetEnvPrefix("PIPELINECONDUCTOR")
	viper.AutomaticEnv()

	// Also check for GITHUB_TOKEN without prefix
	if viper.GetString("github_token") == "" {
		if token := os.Getenv("GITHUB_TOKEN"); token != "" {
			viper.Set("github_token", token)
		}
	}

	if err := viper.ReadInConfig(); err == nil {
		if viper.GetBool("verbose") {
			fmt.Fprintln(os.Stderr, "Using config file:", viper.ConfigFileUsed())
		}
	}
}
