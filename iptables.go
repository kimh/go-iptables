package iptables

// This package is originally from Docker and has been modified for use by the
// Flynn project. See the NOTICE and LICENSE files for licensing and copyright
// details.

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

type Action string

const (
	Add    Action = "-A"
	Delete Action = "-D"
)

var (
	ErrIptablesNotFound = errors.New("Iptables not found")
	nat                 = []string{"-t", "nat"}
	supportsXlock       = false
)

type Chain struct {
	Name   string
	Bridge string
}

func init() {
	supportsXlock = exec.Command("iptables", "--wait", "-L", "-n").Run() == nil
}

func NewChain(name, bridge string) (*Chain, error) {
	if output, err := Raw("-t", "nat", "-N", name); err != nil {
		return nil, err
	} else if len(output) != 0 {
		return nil, fmt.Errorf("Error creating new iptables chain: %s", output)
	}
	chain := &Chain{
		Name:   name,
		Bridge: bridge,
	}

	if err := chain.Prerouting(Add, "-m", "addrtype", "--dst-type", "LOCAL"); err != nil {
		return nil, fmt.Errorf("Failed to inject update PREROUTING chain: %s", err)
	}
	if err := chain.Output(Add, "-m", "addrtype", "--dst-type", "LOCAL", "!", "--dst", "127.0.0.0/8"); err != nil {
		return nil, fmt.Errorf("Failed to inject update OUTPUT chain: %s", err)
	}
	return chain, nil
}

func RemoveExistingChain(name string) error {
	chain := &Chain{
		Name: name,
	}
	return chain.Remove()
}

func (c *Chain) Forward(action Action, ip net.IP, port int, proto, destAddr string, destPort int) error {
	daddr := ip.String()
	if ip.IsUnspecified() {
		// iptables interprets "0.0.0.0" as "0.0.0.0/32", whereas we
		// want "0.0.0.0/0". "0/0" is correctly interpreted as "any
		// value" by both iptables and ip6tables.
		daddr = "0/0"
	}
	if output, err := Raw("-t", "nat", fmt.Sprint(action), c.Name,
		"-p", proto,
		"-d", daddr,
		"--dport", strconv.Itoa(port),
		"-j", "DNAT",
		"--to-destination", net.JoinHostPort(destAddr, strconv.Itoa(destPort))); err != nil && action != Delete {
		return err
	} else if len(output) != 0 && action != Delete {
		return fmt.Errorf("Error iptables forward: %s", output)
	}

	fAction := action
	if fAction == Add {
		fAction = "-I"
	}
	if output, err := Raw(string(fAction), "FORWARD",
		"!", "-i", c.Bridge,
		"-o", c.Bridge,
		"-p", proto,
		"-d", destAddr,
		"--dport", strconv.Itoa(destPort),
		"-j", "ACCEPT"); err != nil && action != Delete {
		return err
	} else if len(output) != 0 && action != Delete {
		return fmt.Errorf("Error iptables forward: %s", output)
	}

	if output, err := Raw("-t", "nat", string(fAction), "POSTROUTING",
		"-p", proto,
		"-s", destAddr,
		"-d", destAddr,
		"--dport", strconv.Itoa(destPort),
		"-j", "MASQUERADE"); err != nil && action != Delete {
		return err
	} else if len(output) != 0 && action != Delete {
		return fmt.Errorf("Error iptables forward: %s", output)
	}

	return nil
}

func (c *Chain) Prerouting(action Action, args ...string) error {
	a := append(nat, fmt.Sprint(action), "PREROUTING")
	var to_dest, to_host string
	var _args []string

	for num, arg := range args {
		fmt.Println(arg)
		if arg == "--to-destination" {
			to_dest = arg
			to_host = args[num+1]
			_args = args[0:num]
		}
	}

	if to_dest != "" {
		a = append(a, _args...)
		a = append(a, "-j", c.Name, to_dest, to_host)
	}

	/*
	   fmt.Println("Debug args", a)
	   fmt.Println("Debug to_dest", to_dest)
	*/

	if output, err := Raw(a...); err != nil {
		return err
	} else if len(output) != 0 {
		return fmt.Errorf("Error iptables prerouting: %s", output)
	}
	return nil
}

func (c *Chain) Output(action Action, args ...string) error {
	a := append(nat, fmt.Sprint(action), "OUTPUT")
	if len(args) > 0 {
		a = append(a, args...)
	}
	if output, err := Raw(append(a, "-j", c.Name)...); err != nil {
		return err
	} else if len(output) != 0 {
		return fmt.Errorf("Error iptables output: %s", output)
	}
	return nil
}

func (c *Chain) Remove() error {
	// Ignore errors - This could mean the chains were never set up
	c.Prerouting(Delete, "-m", "addrtype", "--dst-type", "LOCAL")
	c.Output(Delete, "-m", "addrtype", "--dst-type", "LOCAL", "!", "--dst", "127.0.0.0/8")
	c.Output(Delete, "-m", "addrtype", "--dst-type", "LOCAL") // Created in versions <= 0.1.6

	c.Prerouting(Delete)
	c.Output(Delete)

	Raw("-t", "nat", "-F", c.Name)
	Raw("-t", "nat", "-X", c.Name)

	return nil
}

// Check if an existing rule exists
func Exists(args ...string) bool {
	if _, err := Raw(append([]string{"-C"}, args...)...); err != nil {
		return false
	}
	return true
}

func Raw(args ...string) ([]byte, error) {
	path, err := exec.LookPath("iptables")
	if err != nil {
		return nil, ErrIptablesNotFound
	}

	if supportsXlock {
		args = append([]string{"--wait"}, args...)
	}

	if os.Getenv("DEBUG") != "" {
		fmt.Fprintf(os.Stderr, fmt.Sprintf("[debug] %s, %v\n", path, args))
	}

	output, err := exec.Command(path, args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("iptables failed: iptables %v: %s (%s)", strings.Join(args, " "), output, err)
	}

	// ignore iptables' message about xtables lock
	if strings.Contains(string(output), "waiting for it to exit") {
		output = []byte("")
	}

	return output, err
}
