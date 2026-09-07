package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/esrrhs/pingtunnel"
	"gopkg.in/yaml.v3"
)

// loadClientRules reads the client config file. The file is a YAML list of
// strings, each string being the arguments of one client rule, e.g.
//
//	- -l :16121 -s 3.1.4.5 -t 192.168.0.61:22 -tcp 1
//	- -l :16122 -s 3.1.4.5 -t 192.168.0.62:22 -tcp 1
//
// defaults carries the command line values: any parameter a rule does not
// set itself inherits it, so the command line acts as global fallback.
// Process level flags (noprint, nolog, encrypt...) found in a rule are
// accepted and ignored; they are configured on the command line.
func loadClientRules(path string, defaults *clientRule) ([]*clientRule, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var lines []string
	if err := yaml.Unmarshal(data, &lines); err != nil {
		return nil, fmt.Errorf("config file %s must be a YAML list of rule strings: %s", path, err)
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("config file %s contains no rules", path)
	}

	var rules []*clientRule
	usedListen := map[string]bool{}
	for i, line := range lines {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			return nil, fmt.Errorf("config rule %d: empty", i+1)
		}

		r, err := parseClientRule(fields, defaults)
		if err != nil {
			return nil, fmt.Errorf("config rule %d (%s): %s", i+1, line, err)
		}
		if err := validateClientRule(r); err != nil {
			return nil, fmt.Errorf("config rule %d (%s): %s", i+1, line, err)
		}
		if usedListen[r.listen] {
			return nil, fmt.Errorf("config rule %d: duplicate listen addr %s", i+1, r.listen)
		}
		usedListen[r.listen] = true

		rules = append(rules, r)
	}
	return rules, nil
}

func validateClientRule(r *clientRule) error {
	if r.openSock5 != 0 {
		r.tcpmode = 1
	}
	if r.listen == "" {
		return fmt.Errorf("-l is required")
	}
	if r.server == "" {
		return fmt.Errorf("-s is required")
	}
	if r.openSock5 == 0 && r.target == "" {
		return fmt.Errorf("-t is required unless -sock5 is set")
	}
	if r.tcpmodeMaxwin*10 > pingtunnel.FRAME_MAX_ID {
		return fmt.Errorf("set tcp win too big, max = %d", pingtunnel.FRAME_MAX_ID/10)
	}
	return nil
}
