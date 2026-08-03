package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/plexusone/pipelineconductor/internal/policy"
)

var validateCmd = &cobra.Command{
	Use:   "validate [path]",
	Short: "Validate Cedar policy files",
	Long: `Validate Cedar policy syntax and load policies from a directory or file.

Example:
  pipelineconductor validate policies/
  pipelineconductor validate policies/go/merge.cedar
  pipelineconductor validate --builtin`,
	Args: cobra.MaximumNArgs(1),
	RunE: runValidate,
}

func init() {
	rootCmd.AddCommand(validateCmd)

	validateCmd.Flags().Bool("builtin", false, "Validate built-in policies")
	validateCmd.Flags().Bool("verbose", false, "Show policy details")
}

func runValidate(cmd *cobra.Command, args []string) error {
	builtin, _ := cmd.Flags().GetBool("builtin")
	verbose, _ := cmd.Flags().GetBool("verbose")

	engine := policy.NewEngine()
	loader := policy.NewLoader(engine)

	if builtin {
		fmt.Println("Validating built-in policies...")
		if err := loader.LoadBuiltinPolicies(); err != nil {
			return fmt.Errorf("built-in policies invalid: %w", err)
		}
		fmt.Println("✓ Built-in policies are valid")
		return nil
	}

	if len(args) == 0 {
		return fmt.Errorf("path argument required (or use --builtin)")
	}

	path := args[0]
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("path not found: %s", path)
	}

	var count int
	if info.IsDir() {
		fmt.Printf("Validating policies in directory: %s\n", path)
		count, err = validateDirectory(loader, path, verbose)
	} else {
		fmt.Printf("Validating policy file: %s\n", path)
		if err := loader.LoadFromFile(path); err != nil {
			return fmt.Errorf("invalid policy: %w", err)
		}
		count = 1
		if verbose {
			fmt.Printf("  ✓ %s\n", path)
		}
	}

	if err != nil {
		return err
	}

	fmt.Printf("\n✓ %d policy file(s) validated successfully\n", count)
	return nil
}

func validateDirectory(loader *policy.Loader, dir string, verbose bool) (int, error) {
	var count int
	var errors []string

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		if filepath.Ext(path) != ".cedar" {
			return nil
		}

		if err := loader.LoadFromFile(path); err != nil {
			errors = append(errors, fmt.Sprintf("  ✗ %s: %v", path, err))
		} else {
			count++
			if verbose {
				fmt.Printf("  ✓ %s\n", path)
			}
		}

		return nil
	})

	if err != nil {
		return 0, fmt.Errorf("walking directory: %w", err)
	}

	if len(errors) > 0 {
		fmt.Println("\nErrors found:")
		for _, e := range errors {
			fmt.Println(e)
		}
		return count, fmt.Errorf("%d policy file(s) have errors", len(errors))
	}

	return count, nil
}
