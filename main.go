package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/fatih/color"
	multierror "github.com/hashicorp/go-multierror"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/instrumenta/kubeval/kubeval"
	"github.com/instrumenta/kubeval/log"
)

var (
	version     = "dev"
	commit      = "none"
	date        = "unknown"
	directories = []string{}

	// forceColor tells kubeval to use colored output even if
	// stdout is not a TTY
	forceColor bool

	config = kubeval.NewDefaultConfig()
)

// RootCmd represents the the command to run when kubeval is run
var RootCmd = &cobra.Command{
	Use:     "kubeval <file> [file...]",
	Short:   "Validate a Kubernetes YAML file against the relevant schema",
	Long:    `Validate a Kubernetes YAML file against the relevant schema`,
	Version: fmt.Sprintf("Version: %s\nCommit: %s\nDate: %s\n", version, commit, date),
	Run: func(cmd *cobra.Command, args []string) {
		success := true
		windowsStdinIssue := false
		stat, err := os.Stdin.Stat()
		if err != nil {
			// Stat() will return an error on Windows in both Powershell and
			// console until go1.9 when nothing is passed on stdin.
			// See https://github.com/golang/go/issues/14853.
			if runtime.GOOS != "windows" {
				log.Error(err)
				os.Exit(1)
			} else {
				windowsStdinIssue = true
			}
		}
		// Assert that colors will definitely be used if requested
		if forceColor {
			color.NoColor = false
		}
		// We detect whether we have anything on stdin to process if we have no arguments
		// or if the argument is a -
		if (len(args) < 1 || args[0] == "-") && !windowsStdinIssue && ((stat.Mode() & os.ModeCharDevice) == 0) {
			var buffer bytes.Buffer
			scanner := bufio.NewScanner(os.Stdin)
			for scanner.Scan() {
				buffer.WriteString(scanner.Text() + "\n")
			}
			schemaCache := kubeval.NewSchemaCache()
			config.FileName = viper.GetString("filename")
			results, err := kubeval.ValidateWithCache(buffer.Bytes(), schemaCache, config)
			if err != nil {
				log.Error(err)
				os.Exit(1)
			}
			success = logResults(results, success)
		} else {
			if len(args) < 1 && len(directories) < 1 {
				log.Error("You must pass at least one file as an argument, or at least one directory to the directories flag")
				os.Exit(1)
			}
			schemaCache := kubeval.NewSchemaCache()
			files, err := aggregateFiles(args)
			if err != nil {
				log.Error(err.Error())
				success = false
			}
			for _, fileName := range files {
				filePath, _ := filepath.Abs(fileName)
				fileContents, err := ioutil.ReadFile(filePath)
				if err != nil {
					log.Error("Could not open file", fileName)
					earlyExit()
					success = false
					continue
				}
				config.FileName = fileName
				results, err := kubeval.ValidateWithCache(fileContents, schemaCache, config)
				if err != nil {
					log.Error(err)
					earlyExit()
					success = false
					continue
				}
				success = logResults(results, success)
			}
		}
		if !success {
			os.Exit(1)
		}
	},
}

func logResults(results []kubeval.ValidationResult, success bool) bool {
	for _, result := range results {
		if len(result.Errors) > 0 {
			success = false
			log.Warn("The file", result.FileName, "contains an invalid", result.Kind)
			for _, desc := range result.Errors {
				log.Info("--->", desc)
			}
		} else if result.Kind == "" {
			log.Success("The file", result.FileName, "contains an empty YAML document")
		} else if !result.ValidatedAgainstSchema {
			log.Warn("The file", result.FileName, "containing a", result.Kind, "was not validated against a schema")
		} else {
			log.Success("The file", result.FileName, "contains a valid", result.Kind)
		}
	}
	return success
}

func aggregateFiles(args []string) ([]string, error) {
	files := make([]string, len(args))
	copy(files, args)

	var allErrors *multierror.Error
	for _, directory := range directories {
		err := filepath.Walk(directory, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() && strings.HasSuffix(info.Name(), ".yaml") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			allErrors = multierror.Append(allErrors, err)
		}
	}

	return files, allErrors.ErrorOrNil()
}

func earlyExit() {
	if config.ExitOnError {
		os.Exit(1)
	}
}

// Execute adds all child commands to the root command sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Error(err)
		os.Exit(-1)
	}
}

func init() {
	kubeval.AddKubevalFlags(RootCmd, config)
	RootCmd.Flags().BoolVarP(&forceColor, "force-color", "", false, "Force colored output even if stdout is not a TTY")
	RootCmd.SetVersionTemplate(`{{.Version}}`)
	RootCmd.Flags().StringSliceVarP(&directories, "directories", "d", []string{}, "A comma-separated list of directories to recursively search for YAML documents")

	viper.SetEnvPrefix("KUBEVAL")
	viper.AutomaticEnv()
	viper.BindPFlag("schema_location", RootCmd.Flags().Lookup("schema-location"))
	viper.BindPFlag("filename", RootCmd.Flags().Lookup("filename"))
}

func main() {
	Execute()
}
