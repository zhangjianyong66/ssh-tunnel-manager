package portdiscovery

import "testing"

func TestParseSSOutputDeduplicatesSortsAndKeepsProcess(t *testing.T) {
	output := `State Recv-Q Send-Q Local Address:Port Peer Address:Port Process
LISTEN 0 128 127.0.0.1:8080 0.0.0.0:* users:(("node",pid=10,fd=3))
LISTEN 0 4096 *:22 *:* users:(("sshd",pid=2,fd=4))
LISTEN 0 128 [::]:8080 [::]:*
LISTEN 0 128 [::1]:631 [::]:* users:(("cupsd",pid=3,fd=5))
ESTAB 0 0 127.0.0.1:9999 127.0.0.1:50000`
	ports, diagnostics := Parse(output)
	if len(ports) != 3 {
		t.Fatalf("ports = %#v", ports)
	}
	if ports[0].Number != 22 || ports[0].Process != "sshd" || ports[1].Number != 631 || ports[2].Number != 8080 || ports[2].Process != "node" {
		t.Fatalf("unexpected ports = %#v", ports)
	}
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func TestParseSSOutputSkipsInvalidPorts(t *testing.T) {
	output := "LISTEN 0 128 *:0 *:*\nLISTEN 0 128 *:65536 *:*\nLISTEN 0 128 invalid *:*\n"
	ports, diagnostics := Parse(output)
	if len(ports) != 0 || len(diagnostics) != 3 {
		t.Fatalf("ports = %#v, diagnostics = %#v", ports, diagnostics)
	}
}
