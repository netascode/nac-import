package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/alexflint/go-arg"
	"golang.org/x/term"
)

type args struct {
	Verbose   bool `arg:"-v,--verbose" help:"Print debug output to CLI"`
	NoCleanup bool `arg:"--no-cleanup" help:"Keep temporary files for RCA"`
	Install   bool `arg:"--install"    help:"Install terraform if not found locally"`
}

func (args) Description() string {
	return "ACI as Code terraform import tool"
}

func (args) Version() string {
	return fmt.Sprintf("%s - commit %s - %s", version, commit, date)
}

func getInput(prompt string) string {
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("%s ", prompt)
	input, _ := reader.ReadString('\n')
	return strings.Trim(input, "\r\n")
}

func getPassword(prompt string) string {
	fmt.Print(prompt + " ")
	pwd, _ := term.ReadPassword(int(syscall.Stdin))
	return string(pwd)
}

func getArgs() args {
	a := args{}
	arg.MustParse(&a)
	return a
}
