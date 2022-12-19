// Package main
package main

import (
	"context"
	_ "encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aci-vetr/bats/logger"
	"github.com/alitto/pond"
	ver "github.com/hashicorp/go-version"
	"github.com/hashicorp/hc-install/fs"
	"github.com/hashicorp/hc-install/product"
	"github.com/hashicorp/hc-install/releases"
	"github.com/hashicorp/terraform-exec/tfexec"
)

const (
	versionConstraint = ">= 1.3"
	exactVersion      = "1.3.6"
	workerCount       = 10
	planFile          = "aac-import.tfplan"
	cleanupPath       = "aac-import-files"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// installBinary installs a temporary terraform binary
func installBinary(exectVersion string) (string, error) {
	installer := &releases.ExactVersion{
		Product: product.Terraform,
		Version: ver.Must(ver.NewVersion(exactVersion)),
	}
	return installer.Install(context.Background())
}

// findBinary locates an existing terraform binary
func findBinary(constraint string) (string, error) {
	vc, _ := ver.NewConstraint(">= 1.3")
	versions := &fs.Version{
		Product:     product.Terraform,
		Constraints: vc,
	}
	return versions.Find(context.Background())
}

// mo represents an object pending import
type mo struct {
	addr      string
	id        string
	stateFile string
}

// newMO creates a new MO to be imported
func newMO(addr, class, dn string) mo {
	id := class + ":" + dn
	name := id
	for _, r := range []string{":", "[", "]", `"`, "'", "(", ")", "/"} {
		name = strings.ReplaceAll(name, r, "_")
	}

	return mo{
		addr:      addr,
		id:        class + ":" + dn,
		stateFile: name + ".tmp.tfstate",
	}
}

// getPlan runs terraform plan and saves to a temporary file
func getPlan(tf *tfexec.Terraform) error {
	log := logger.Get()
	log.Info().Msg("Running terraform plan")
	hasChanges, err := tf.Plan(context.Background(), tfexec.Out(planFile))
	if err != nil {
		return fmt.Errorf("cannot run terraform plan: %v", err)
	}
	if hasChanges == false {
		log.Info().Msg("No changes")
	}
	return nil
}

// getPlannedChanges pulls changes from terraform plan output
func getPlannedChanges(tf *tfexec.Terraform) ([]mo, error) {
	log := logger.Get()
	log.Info().Msg("Analyzing terraform plan output")
	mos := []mo{}
	plan, err := tf.ShowPlanFile(context.Background(), planFile)
	if err != nil {
		return mos, err
	}
	for _, change := range plan.ResourceChanges {
		if change.Type != "aci_rest_managed" {
			continue
		}
		if !change.Change.Actions.Create() {
			continue
		}
		fmt.Println(change)
		// address := change.Address
		after, ok := change.Change.After.(map[string]interface{})
		if !ok {
			continue
		}
		dn, ok := after["dn"].(string)
		if !ok {
			continue
		}
		class, ok := after["class_name"].(string)
		if !ok {
			continue
		}

		mos = append(mos, newMO(change.Address, class, dn))
	}
	log.Info().Msgf("Found %d importable changes in plan", len(mos))
	return mos, nil
}

// getPreRunFiles finds files in the workingDir before the tool runs
func getPreRunFiles(workingDir string) map[string]struct{} {
	files := map[string]struct{}{}
	dir, _ := os.ReadDir(workingDir)
	for _, file := range dir {
		files[file.Name()] = struct{}{}
	}
	return files
}

// cleanup cleans up temp files
func cleanup(workingDir string, preRunFiles map[string]struct{}, mv bool) {
	dir, _ := os.ReadDir(workingDir)
	for _, file := range dir {
		if file.Name() == "terraform.tfstate" {
			continue
		}
		if _, ok := preRunFiles[file.Name()]; ok {
			continue
		}
		path := filepath.Join(workingDir, file.Name())
		if mv {
			dst := filepath.Join(workingDir, cleanupPath, file.Name())
			os.Rename(path, dst)
			continue
		}
		os.RemoveAll(path)
	}
}

func mainHandler(log *logger.Logger, args args) error {
	workingDir, _ := os.Getwd()
	// Cleanup any new files created other than terraform.tfstate
	preRunFiles := getPreRunFiles(workingDir)
	defer cleanup(workingDir, preRunFiles, args.NoCleanup)

	mainStateFile := filepath.Join(workingDir, "terraform.tfstate")

	log.Info().Msg("Validating terraform installation")
	execPath, err := findBinary(versionConstraint)
	if err != nil {
		if !args.Install {
			return fmt.Errorf(
				"unable to find terraform install for version %s: %v",
				versionConstraint,
				err,
			)
		}
		log.Warn().Msgf("unable to find a terraform install for version %s: %v",
			versionConstraint,
			err,
		)
		execPath, err = installBinary(exactVersion)
		if err != nil {
			return fmt.Errorf("unable to install terraform %s: %v", exactVersion, err)
		}
	}
	tf, err := tfexec.NewTerraform(workingDir, execPath)
	if err != nil {
		return fmt.Errorf("error accessing terraform binary: %v", err)
	}

	// terraform init
	log.Info().Msg("Running terraform init")
	if err := tf.Init(context.Background()); err != nil {
		return fmt.Errorf("cannot run terraform init: %v", err)
	}

	// terraform plan
	if err := getPlan(tf); err != nil {
		return err
	}

	// terraform show
	mos, err := getPlannedChanges(tf)
	if err != nil {
		return fmt.Errorf("cannot show plan file: %v", err)
	}
	if len(mos) == 0 {
		return nil
	}

	// Import to separate files to avoid state file locks
	log.Info().Msgf("Importing %d objects...", len(mos))
	pool := pond.New(workerCount, 0, pond.MinWorkers(workerCount))
	for i, mo := range mos {
		i, mo := i, mo
		pool.Submit(func() {
			log.Info().Int("progress", i).Msgf("Importing %s", mo.id)
			log.Debug().
				Str("addr", mo.addr).
				Str("id", mo.id).
				Str("stateFile", mo.stateFile).
				Msg("importing")
			// Back off and retry when hitting APIC throttling limits
			var err error
			for try := 0; try < 3; try++ {
				err = tf.Import(
					context.Background(),
					mo.addr,
					mo.id,
					tfexec.StateOut(mo.stateFile),
					tfexec.Lock(false),
				)
				if err == nil {
					break
				}
				time.Sleep(time.Second * 1)
			}
			if err != nil {
				log.Warn().Err(err).Msgf("could not import %s %s", mo.addr, mo.id)
			}
		})
	}
	pool.StopAndWait()

	// Move state to actual state file
	// This has to run sequentially to avoid file lock issues
	// It's local so faster than imports
	log.Info().Msg("Merging state...")
	hasStateFile := false
	if _, err := os.Stat(mainStateFile); err == nil {
		hasStateFile = true
	}
	for _, mo := range mos {
		log.Debug().Interface("mo", mo).Msg("merging")
		if _, err := os.Stat(mo.stateFile); err != nil {
			log.Debug().Msgf("no temporary state file %s", mo.stateFile)
			continue
		}
		if !hasStateFile {
			os.Rename(mo.stateFile, mainStateFile)
			hasStateFile = true
			continue
		}
		log.Info().Msgf("Merging %s", mo.addr)
		var err error
		for try := 0; try < 3; try++ {
			err = tf.StateMv(
				context.Background(),
				mo.addr,
				mo.addr,
				tfexec.State(mo.stateFile),
				tfexec.StateOut(mainStateFile),
				tfexec.Lock(false),
				tfexec.Backup("aac-import.tmp.tfstate.backup"),
			)
			if err == nil {
				break
			}
		}
		log.Warn().Err(err).Msgf("could not merge %s", mo.addr)
	}
	return nil
}

func main() {
	args := getArgs()
	consoleLevel := logger.InfoLevel
	if args.Verbose {
		consoleLevel = logger.DebugLevel
	}
	log, _ := logger.New(logger.Config{
		Filename:     "aac-import.log",
		ConsoleLevel: consoleLevel,
	})
	if err := mainHandler(log, args); err != nil {
		log.Fatal().Err(err).Msg("Operation failed")
	}
}
