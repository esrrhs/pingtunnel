package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfigFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadClientRulesMultiple(t *testing.T) {
	path := writeConfigFile(t, `- -l :16121 -s 3.1.4.5 -t 192.168.0.61:22 -tcp 1
- -l :16122 -s 3.1.4.5 -t 192.168.0.62:22 -tcp 1 -timeout 120 -key 7
- -l :16123 -s 3.1.4.5 -t 192.168.0.63:22 -tcp 1
`)
	rules, err := loadClientRules(path, nil)
	if err != nil {
		t.Fatalf("loadClientRules error: %v", err)
	}
	if len(rules) != 3 {
		t.Fatalf("expect 3 rules, got %d", len(rules))
	}

	r := rules[0]
	if r.listen != ":16121" || r.server != "3.1.4.5" || r.target != "192.168.0.61:22" || r.tcpmode != 1 {
		t.Errorf("rule 0 mismatch: %+v", r)
	}

	r = rules[1]
	if r.listen != ":16122" || r.target != "192.168.0.62:22" || r.timeout != 120 || r.key != 7 {
		t.Errorf("rule 1 mismatch: %+v", r)
	}

	r = rules[2]
	if r.listen != ":16123" || r.target != "192.168.0.63:22" {
		t.Errorf("rule 2 mismatch: %+v", r)
	}
}

func TestLoadClientRulesProcessLevelFlagsIgnored(t *testing.T) {
	path := writeConfigFile(t, `- -l :16121 -s 3.1.4.5 -t 10.0.0.1:22 -tcp 1 -noprint 1 -nolog 1 -loglevel debug -encrypt aes256 -encrypt-key secret
`)
	rules, err := loadClientRules(path, nil)
	if err != nil {
		t.Fatalf("process level flags in rule should be ignored, got error: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expect 1 rule, got %d", len(rules))
	}
	if rules[0].listen != ":16121" || rules[0].target != "10.0.0.1:22" || rules[0].tcpmode != 1 {
		t.Errorf("rule mismatch: %+v", rules[0])
	}
}

func TestLoadClientRulesDefaults(t *testing.T) {
	def := &clientRule{timeout: 120, key: 5, tcpmode: 1, tcpmodeBuffersize: 2048}
	path := writeConfigFile(t, `- -l :16121 -s 3.1.4.5 -t 10.0.0.1:22
`)
	rules, err := loadClientRules(path, def)
	if err != nil {
		t.Fatalf("loadClientRules error: %v", err)
	}
	r := rules[0]
	if r.timeout != 120 || r.key != 5 || r.tcpmode != 1 || r.tcpmodeBuffersize != 2048 {
		t.Errorf("defaults not inherited: %+v", r)
	}
}

func TestLoadClientRulesFlagDefaults(t *testing.T) {
	path := writeConfigFile(t, `- -l :16121 -s 3.1.4.5 -t 10.0.0.1:22
`)
	rules, err := loadClientRules(path, nil)
	if err != nil {
		t.Fatalf("loadClientRules error: %v", err)
	}
	r := rules[0]
	if r.timeout != 60 || r.tcpmode != 0 || r.openSock5 != 0 || r.maxconn != 0 {
		t.Errorf("flag defaults mismatch: %+v", r)
	}
}

func TestLoadClientRulesSock5(t *testing.T) {
	path := writeConfigFile(t, `- -l :16121 -s 3.1.4.5 -sock5 1 -s5user u -s5pass p
`)
	rules, err := loadClientRules(path, nil)
	if err != nil {
		t.Fatalf("loadClientRules error: %v", err)
	}
	r := rules[0]
	if r.openSock5 != 1 || r.sock5User != "u" || r.sock5Pass != "p" || r.target != "" {
		t.Errorf("sock5 rule mismatch: %+v", r)
	}
	if r.tcpmode != 1 {
		t.Errorf("sock5 rule should force tcpmode 1: %+v", r)
	}
}

func TestLoadClientRulesMissingListen(t *testing.T) {
	path := writeConfigFile(t, `- -s 3.1.4.5 -t 10.0.0.1:22
`)
	_, err := loadClientRules(path, nil)
	if err == nil {
		t.Fatal("expect error for missing -l")
	}
}

func TestLoadClientRulesMissingServer(t *testing.T) {
	path := writeConfigFile(t, `- -l :16121 -t 10.0.0.1:22
`)
	_, err := loadClientRules(path, nil)
	if err == nil {
		t.Fatal("expect error for missing -s")
	}
}

func TestLoadClientRulesMissingTarget(t *testing.T) {
	path := writeConfigFile(t, `- -l :16121 -s 3.1.4.5
`)
	_, err := loadClientRules(path, nil)
	if err == nil {
		t.Fatal("expect error for missing -t in non-sock5 rule")
	}
}

func TestLoadClientRulesDuplicateListen(t *testing.T) {
	path := writeConfigFile(t, `- -l :16121 -s 3.1.4.5 -t 10.0.0.1:22 -tcp 1
- -l :16121 -s 3.1.4.5 -t 10.0.0.2:22 -tcp 1
`)
	_, err := loadClientRules(path, nil)
	if err == nil {
		t.Fatal("expect error for duplicate -l")
	}
}

func TestLoadClientRulesUnknownFlag(t *testing.T) {
	path := writeConfigFile(t, `- -l :16121 -s 3.1.4.5 -t 10.0.0.1:22 -foo 1
`)
	_, err := loadClientRules(path, nil)
	if err == nil {
		t.Fatal("expect error for unknown flag")
	}
}

func TestLoadClientRulesMaxWinTooBig(t *testing.T) {
	path := writeConfigFile(t, `- -l :16121 -s 3.1.4.5 -t 10.0.0.1:22 -tcp 1 -tcp_mw 200001
`)
	_, err := loadClientRules(path, nil)
	if err == nil {
		t.Fatal("expect error for tcp_mw too big")
	}
}

func TestLoadClientRulesEmptyList(t *testing.T) {
	path := writeConfigFile(t, `# no rules
`)
	_, err := loadClientRules(path, nil)
	if err == nil {
		t.Fatal("expect error for empty config")
	}
}

func TestLoadClientRulesNotList(t *testing.T) {
	path := writeConfigFile(t, `rules: foo
`)
	_, err := loadClientRules(path, nil)
	if err == nil {
		t.Fatal("expect error for non-list config")
	}
}

func TestLoadClientRulesMissingFile(t *testing.T) {
	_, err := loadClientRules(filepath.Join(t.TempDir(), "notexist.yaml"), nil)
	if err == nil {
		t.Fatal("expect error for missing file")
	}
}
