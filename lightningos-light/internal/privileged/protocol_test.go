package privileged

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"lightningos-light/internal/appmanifest"
)

func TestDecodeRequestStrictValidation(t *testing.T) {
	valid := `{"version":1,"request_id":"request_1","operation":"service.restart","dry_run":true,"params":{"unit":"lnd","no_block":true}}`
	tests := []struct {
		name    string
		payload string
		wantErr bool
	}{
		{name: "valid", payload: valid},
		{name: "unknown top field", payload: `{"version":1,"request_id":"request_1","operation":"self_test","params":{},"command":"/bin/sh"}`, wantErr: true},
		{name: "unsupported version", payload: `{"version":2,"request_id":"request_1","operation":"self_test","params":{}}`, wantErr: true},
		{name: "invalid id", payload: `{"version":1,"request_id":"../../bad","operation":"self_test","params":{}}`, wantErr: true},
		{name: "unknown operation", payload: `{"version":1,"request_id":"request_1","operation":"/bin/sh","params":{}}`, wantErr: true},
		{name: "missing params", payload: `{"version":1,"request_id":"request_1","operation":"self_test"}`, wantErr: true},
		{name: "null params", payload: `{"version":1,"request_id":"request_1","operation":"self_test","params":null}`, wantErr: true},
		{name: "unknown params", payload: `{"version":1,"request_id":"request_1","operation":"service.restart","params":{"unit":"lnd","args":["; reboot"]}}`, wantErr: true},
		{name: "file path injection", payload: `{"version":1,"request_id":"request_1","operation":"files.enable_login","params":{"path":"/etc/shadow"}}`, wantErr: true},
		{name: "file content injection", payload: `{"version":1,"request_id":"request_1","operation":"files.enable_login","params":{"content":"root shell"}}`, wantErr: true},
		{name: "valid app lifecycle", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.lifecycle","params":{"app_id":"cpuminer","action":"start"}}`},
		{name: "valid robosats lifecycle", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.lifecycle","params":{"app_id":"robosats","action":"stop"}}`},
		{name: "valid bitcoin lifecycle restart", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.lifecycle","params":{"app_id":"bitcoincore","action":"restart"}}`},
		{name: "valid btcpay lifecycle", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.lifecycle","params":{"app_id":"btcpay","action":"start"}}`},
		{name: "valid lndg lifecycle", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.lifecycle","params":{"app_id":"lndg","action":"start"}}`},
		{name: "btcpay restart rejected", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.lifecycle","params":{"app_id":"btcpay","action":"restart"}}`, wantErr: true},
		{name: "restart restricted to bitcoin", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.lifecycle","params":{"app_id":"cpuminer","action":"restart"}}`, wantErr: true},
		{name: "valid mempool lifecycle", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.lifecycle","params":{"app_id":"mempool","action":"start"}}`},
		{name: "unknown app", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.lifecycle","params":{"app_id":"unknown","action":"start"}}`, wantErr: true},
		{name: "shell app", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.lifecycle","params":{"app_id":"cpuminer;reboot","action":"start"}}`, wantErr: true},
		{name: "unknown app action", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.lifecycle","params":{"app_id":"cpuminer","action":"exec"}}`, wantErr: true},
		{name: "app argument injection", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.lifecycle","params":{"app_id":"cpuminer","action":"start","args":["--privileged"]}}`, wantErr: true},
		{name: "app path injection", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.lifecycle","params":{"app_id":"cpuminer","action":"start","compose_path":"/tmp/evil.yaml"}}`, wantErr: true},
		{name: "valid btcpay snapshot", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.snapshot","params":{"app_id":"btcpay"}}`},
		{name: "valid btcpay snapshot dry run", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.snapshot","dry_run":true,"params":{"app_id":"btcpay"}}`},
		{name: "snapshot restricted to btcpay", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.snapshot","params":{"app_id":"robosats"}}`, wantErr: true},
		{name: "snapshot argument injection", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.snapshot","params":{"app_id":"btcpay","compose_path":"/tmp/evil.yaml"}}`, wantErr: true},
		{name: "valid app inspect", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.inspect","params":{"app_id":"cpuminer"}}`},
		{name: "valid robosats inspect", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.inspect","params":{"app_id":"robosats"}}`},
		{name: "valid bitcoin inspect", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.inspect","params":{"app_id":"bitcoincore"}}`},
		{name: "valid btcpay inspect", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.inspect","params":{"app_id":"btcpay"}}`},
		{name: "valid lndg inspect", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.inspect","params":{"app_id":"lndg"}}`},
		{name: "app inspect dry run", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.inspect","dry_run":true,"params":{"app_id":"cpuminer"}}`, wantErr: true},
		{name: "valid mempool inspect", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.inspect","params":{"app_id":"mempool"}}`},
		{name: "unknown app inspect", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.inspect","params":{"app_id":"unknown"}}`, wantErr: true},
		{name: "app inspect argument injection", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.inspect","params":{"app_id":"cpuminer","args":["--privileged"]}}`, wantErr: true},
		{name: "app inspect path injection", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.inspect","params":{"app_id":"cpuminer","compose_path":"/tmp/evil.yaml"}}`, wantErr: true},
		{name: "valid Fedimint Guardian logs", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.logs","params":{"app_id":"fedimint-guardian","lines":200,"since":"2h"}}`},
		{name: "valid Fedimint Gateway logs", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.logs","params":{"app_id":"fedimint-gateway","lines":500}}`},
		{name: "valid Bitcoin Core logs", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.logs","params":{"app_id":"bitcoincore","lines":200,"since":"2h"}}`},
		{name: "Fedimint logs dry run", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.logs","dry_run":true,"params":{"app_id":"fedimint-guardian","lines":200}}`, wantErr: true},
		{name: "Fedimint logs app injection", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.logs","params":{"app_id":"fedimint-guardian;reboot","lines":200}}`, wantErr: true},
		{name: "Fedimint logs option injection", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.logs","params":{"app_id":"fedimint-gateway","lines":200,"since":"--all"}}`, wantErr: true},
		{name: "Fedimint logs argument injection", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.logs","params":{"app_id":"fedimint-gateway","lines":200,"args":["--follow"]}}`, wantErr: true},
		{name: "valid app remove", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.remove","params":{"app_id":"cpuminer"}}`},
		{name: "valid robosats remove", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.remove","params":{"app_id":"robosats"}}`},
		{name: "valid bitcoin remove", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.remove","params":{"app_id":"bitcoincore"}}`},
		{name: "valid btcpay remove", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.remove","params":{"app_id":"btcpay"}}`},
		{name: "valid lndg remove", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.remove","params":{"app_id":"lndg"}}`},
		{name: "valid app remove dry run", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.remove","dry_run":true,"params":{"app_id":"cpuminer"}}`},
		{name: "valid mempool remove", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.remove","params":{"app_id":"mempool"}}`},
		{name: "unknown app remove", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.remove","params":{"app_id":"unknown"}}`, wantErr: true},
		{name: "app remove argument injection", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.remove","params":{"app_id":"cpuminer","args":["--volumes"]}}`, wantErr: true},
		{name: "app remove path injection", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.remove","params":{"app_id":"cpuminer","compose_path":"/tmp/evil.yaml"}}`, wantErr: true},
		{name: "valid docker ensure", payload: `{"version":1,"request_id":"request_1","operation":"docker.runtime.ensure","params":{}}`},
		{name: "valid docker ensure dry run", payload: `{"version":1,"request_id":"request_1","operation":"docker.runtime.ensure","dry_run":true,"params":{}}`},
		{name: "docker ensure arguments", payload: `{"version":1,"request_id":"request_1","operation":"docker.runtime.ensure","params":{"packages":["docker.io"]}}`, wantErr: true},
		{name: "valid docker status", payload: `{"version":1,"request_id":"request_1","operation":"docker.runtime.status","params":{}}`},
		{name: "docker status dry run", payload: `{"version":1,"request_id":"request_1","operation":"docker.runtime.status","dry_run":true,"params":{}}`, wantErr: true},
		{name: "valid bitcoin consumer network ensure", payload: `{"version":1,"request_id":"request_1","operation":"bitcoin.consumer-network.ensure","params":{}}`},
		{name: "valid bitcoin consumer network dry run", payload: `{"version":1,"request_id":"request_1","operation":"bitcoin.consumer-network.ensure","dry_run":true,"params":{}}`},
		{name: "bitcoin consumer network arguments", payload: `{"version":1,"request_id":"request_1","operation":"bitcoin.consumer-network.ensure","params":{"subnet":"0.0.0.0/0"}}`, wantErr: true},
		{name: "valid Loop status", payload: `{"version":1,"request_id":"request_1","operation":"app.loop.status","params":{}}`},
		{name: "Loop status dry run", payload: `{"version":1,"request_id":"request_1","operation":"app.loop.status","dry_run":true,"params":{}}`, wantErr: true},
		{name: "valid Loop ensure", payload: `{"version":1,"request_id":"request_1","operation":"app.loop.ensure","params":{"lnd_tls_certificate":"Y2VydA==","lnd_macaroon":"bWFj"}}`},
		{name: "Loop ensure empty certificate", payload: `{"version":1,"request_id":"request_1","operation":"app.loop.ensure","params":{"lnd_tls_certificate":""}}`, wantErr: true},
		{name: "Loop ensure path injection", payload: `{"version":1,"request_id":"request_1","operation":"app.loop.ensure","params":{"lnd_tls_certificate":"Y2VydA==","path":"/etc/shadow"}}`, wantErr: true},
		{name: "Loop ensure URL injection", payload: `{"version":1,"request_id":"request_1","operation":"app.loop.ensure","params":{"lnd_tls_certificate":"Y2VydA==","url":"https://evil.invalid/loop"}}`, wantErr: true},
		{name: "valid Loop start", payload: `{"version":1,"request_id":"request_1","operation":"app.loop.lifecycle","params":{"action":"start"}}`},
		{name: "valid Loop stop", payload: `{"version":1,"request_id":"request_1","operation":"app.loop.lifecycle","params":{"action":"stop"}}`},
		{name: "Loop restart rejected", payload: `{"version":1,"request_id":"request_1","operation":"app.loop.lifecycle","params":{"action":"restart"}}`, wantErr: true},
		{name: "Loop command injection", payload: `{"version":1,"request_id":"request_1","operation":"app.loop.lifecycle","params":{"action":"start","command":"/bin/sh"}}`, wantErr: true},
		{name: "valid Loop remove", payload: `{"version":1,"request_id":"request_1","operation":"app.loop.remove","params":{}}`},
		{name: "Loop remove path injection", payload: `{"version":1,"request_id":"request_1","operation":"app.loop.remove","params":{"path":"/"}}`, wantErr: true},
		{name: "valid Elements status", payload: `{"version":1,"request_id":"request_1","operation":"app.elements.status","params":{"data_dir":"/data/elements"}}`},
		{name: "Elements status dry run", payload: `{"version":1,"request_id":"request_1","operation":"app.elements.status","dry_run":true,"params":{"data_dir":"/data/elements"}}`, wantErr: true},
		{name: "valid Elements ensure", payload: `{"version":1,"request_id":"request_1","operation":"app.elements.ensure","dry_run":true,"params":{"data_dir":"/data/elements","content":"chain=liquidv1\ndaemon=0\nserver=1\nrpcbind=127.0.0.1\nrpcallowip=127.0.0.1\nrpcport=7041\n"}}`},
		{name: "Elements public RPC rejected", payload: `{"version":1,"request_id":"request_1","operation":"app.elements.ensure","params":{"data_dir":"/data/elements","content":"chain=liquidv1\ndaemon=0\nserver=1\nrpcbind=0.0.0.0\nrpcallowip=127.0.0.1\nrpcport=7041\n"}}`, wantErr: true},
		{name: "Elements path injection", payload: `{"version":1,"request_id":"request_1","operation":"app.elements.status","params":{"data_dir":"/data/elements;reboot"}}`, wantErr: true},
		{name: "valid Elements stop", payload: `{"version":1,"request_id":"request_1","operation":"app.elements.lifecycle","params":{"data_dir":"/data/elements","action":"stop"}}`},
		{name: "Elements restart rejected", payload: `{"version":1,"request_id":"request_1","operation":"app.elements.lifecycle","params":{"data_dir":"/data/elements","action":"restart"}}`, wantErr: true},
		{name: "valid package ensure", payload: `{"version":1,"request_id":"request_1","operation":"packages.feature.ensure","params":{"feature":"docker_runtime"}}`},
		{name: "valid package ensure dry run", payload: `{"version":1,"request_id":"request_1","operation":"packages.feature.ensure","dry_run":true,"params":{"feature":"docker_runtime"}}`},
		{name: "valid package status", payload: `{"version":1,"request_id":"request_1","operation":"packages.feature.status","params":{"feature":"docker_runtime"}}`},
		{name: "package status dry run", payload: `{"version":1,"request_id":"request_1","operation":"packages.feature.status","dry_run":true,"params":{"feature":"docker_runtime"}}`, wantErr: true},
		{name: "unknown package feature", payload: `{"version":1,"request_id":"request_1","operation":"packages.feature.ensure","params":{"feature":"docker_runtime;reboot"}}`, wantErr: true},
		{name: "package argument injection", payload: `{"version":1,"request_id":"request_1","operation":"packages.feature.ensure","params":{"feature":"docker_runtime","packages":["docker.io"]}}`, wantErr: true},
		{name: "valid image prepare", payload: `{"version":1,"request_id":"request_1","operation":"app.image.prepare","params":{"app_id":"cpuminer","variant":"baseline"}}`},
		{name: "valid robosats image prepare", payload: `{"version":1,"request_id":"request_1","operation":"app.image.prepare","params":{"app_id":"robosats","variant":"client"}}`},
		{name: "valid bitcoin core image prepare", payload: `{"version":1,"request_id":"request_1","operation":"app.image.prepare","params":{"app_id":"bitcoincore","variant":"node"}}`},
		{name: "valid btcpay latest image prepare", payload: `{"version":1,"request_id":"request_1","operation":"app.image.prepare","params":{"app_id":"btcpay","variant":"server"}}`},
		{name: "valid btcpay dependency image status", payload: `{"version":1,"request_id":"request_1","operation":"app.image.status","params":{"app_id":"btcpay","variant":"nbxplorer"}}`},
		{name: "valid lndg app image prepare", payload: `{"version":1,"request_id":"request_1","operation":"app.image.prepare","params":{"app_id":"lndg","variant":"app"}}`},
		{name: "valid mempool frontend image prepare", payload: `{"version":1,"request_id":"request_1","operation":"app.image.prepare","params":{"app_id":"mempool","variant":"frontend"}}`},
		{name: "valid mempool backend image probe", payload: `{"version":1,"request_id":"request_1","operation":"app.image.probe","params":{"app_id":"mempool","variant":"backend"}}`},
		{name: "valid lndg postgres image status", payload: `{"version":1,"request_id":"request_1","operation":"app.image.status","params":{"app_id":"lndg","variant":"postgres"}}`},
		{name: "valid image prepare dry run", payload: `{"version":1,"request_id":"request_1","operation":"app.image.prepare","dry_run":true,"params":{"app_id":"cpuminer","variant":"fast_pinned"}}`},
		{name: "valid image status", payload: `{"version":1,"request_id":"request_1","operation":"app.image.status","params":{"app_id":"cpuminer","variant":"fast_latest"}}`},
		{name: "valid robosats image status", payload: `{"version":1,"request_id":"request_1","operation":"app.image.status","params":{"app_id":"robosats","variant":"proxy"}}`},
		{name: "image status dry run", payload: `{"version":1,"request_id":"request_1","operation":"app.image.status","dry_run":true,"params":{"app_id":"cpuminer","variant":"baseline"}}`, wantErr: true},
		{name: "valid image probe", payload: `{"version":1,"request_id":"request_1","operation":"app.image.probe","params":{"app_id":"cpuminer","variant":"fast_latest"}}`},
		{name: "robosats image probe not allowed", payload: `{"version":1,"request_id":"request_1","operation":"app.image.probe","params":{"app_id":"robosats","variant":"client"}}`, wantErr: true},
		{name: "unknown image app", payload: `{"version":1,"request_id":"request_1","operation":"app.image.prepare","params":{"app_id":"unknown","variant":"baseline"}}`, wantErr: true},
		{name: "unknown image variant", payload: `{"version":1,"request_id":"request_1","operation":"app.image.prepare","params":{"app_id":"cpuminer","variant":"latest"}}`, wantErr: true},
		{name: "btcpay image argument injection", payload: `{"version":1,"request_id":"request_1","operation":"app.image.prepare","params":{"app_id":"btcpay","variant":"server;reboot"}}`, wantErr: true},
		{name: "image argument injection", payload: `{"version":1,"request_id":"request_1","operation":"app.image.prepare","params":{"app_id":"cpuminer","variant":"baseline","image":"evil/root:latest"}}`, wantErr: true},
		{name: "valid robosats firewall ensure", payload: `{"version":1,"request_id":"request_1","operation":"app.firewall.ensure","params":{"app_id":"robosats"}}`},
		{name: "valid robosats firewall dry run", payload: `{"version":1,"request_id":"request_1","operation":"app.firewall.ensure","dry_run":true,"params":{"app_id":"robosats"}}`},
		{name: "valid lndg firewall ensure", payload: `{"version":1,"request_id":"request_1","operation":"app.firewall.ensure","params":{"app_id":"lndg"}}`},
		{name: "valid mempool firewall ensure", payload: `{"version":1,"request_id":"request_1","operation":"app.firewall.ensure","params":{"app_id":"mempool"}}`},
		{name: "valid BTCPay LND host access", payload: `{"version":1,"request_id":"request_1","operation":"app.lnd-host-access.ensure","params":{"app_id":"btcpay"}}`},
		{name: "valid BTCPay LND host access dry run", payload: `{"version":1,"request_id":"request_1","operation":"app.lnd-host-access.ensure","dry_run":true,"params":{"app_id":"btcpay"}}`},
		{name: "LND host access app injection", payload: `{"version":1,"request_id":"request_1","operation":"app.lnd-host-access.ensure","params":{"app_id":"btcpay;reboot"}}`, wantErr: true},
		{name: "LND host access argument injection", payload: `{"version":1,"request_id":"request_1","operation":"app.lnd-host-access.ensure","params":{"app_id":"btcpay","gateway":"0.0.0.0"}}`, wantErr: true},
		{name: "valid lndg admin reset", payload: `{"version":1,"request_id":"request_1","operation":"app.admin.reset","params":{"app_id":"lndg"}}`},
		{name: "valid lndg admin reset dry run", payload: `{"version":1,"request_id":"request_1","operation":"app.admin.reset","dry_run":true,"params":{"app_id":"lndg"}}`},
		{name: "admin reset restricted to lndg", payload: `{"version":1,"request_id":"request_1","operation":"app.admin.reset","params":{"app_id":"btcpay"}}`, wantErr: true},
		{name: "admin reset argument injection", payload: `{"version":1,"request_id":"request_1","operation":"app.admin.reset","params":{"app_id":"lndg","command":"/bin/sh"}}`, wantErr: true},
		{name: "unknown firewall app", payload: `{"version":1,"request_id":"request_1","operation":"app.firewall.ensure","params":{"app_id":"cpuminer"}}`, wantErr: true},
		{name: "firewall port injection", payload: `{"version":1,"request_id":"request_1","operation":"app.firewall.ensure","params":{"app_id":"robosats","port":22}}`, wantErr: true},
		{name: "image unit injection", payload: `{"version":1,"request_id":"request_1","operation":"app.image.prepare","params":{"app_id":"cpuminer","variant":"baseline","unit":"ssh"}}`, wantErr: true},
		{name: "shell unit", payload: `{"version":1,"request_id":"request_1","operation":"service.restart","params":{"unit":"lnd;reboot"}}`, wantErr: true},
		{name: "path unit", payload: `{"version":1,"request_id":"request_1","operation":"service.restart","params":{"unit":"../../lnd"}}`, wantErr: true},
		{name: "unknown unit", payload: `{"version":1,"request_id":"request_1","operation":"service.restart","params":{"unit":"ssh"}}`, wantErr: true},
		{name: "blocking manager restart", payload: `{"version":1,"request_id":"request_1","operation":"service.restart","params":{"unit":"lightningos-manager"}}`, wantErr: true},
		{name: "trailing object", payload: valid + `{}`, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeRequest(strings.NewReader(test.payload))
			if (err != nil) != test.wantErr {
				t.Fatalf("DecodeRequest() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestPeerSwapRequestsAreTypedAndSourceBounded(t *testing.T) {
	request := func(operation Operation, params any, dryRun bool) Request {
		raw, err := MarshalParams(params)
		if err != nil {
			t.Fatal(err)
		}
		return Request{Version: ProtocolVersion, RequestID: "peerswap_test_1", Operation: operation, DryRun: dryRun, Params: raw}
	}
	remote := PeerSwapSource{Mode: appmanifest.PeerSwapElementsModeRemote, URL: "https://elements.example:7041", User: "rpc", Password: "secret", Wallet: "peerswap_node"}
	if err := ValidateRequest(request(OperationPeerSwapSourceWrite, remote, true)); err != nil {
		t.Fatalf("valid remote source rejected: %v", err)
	}
	unsafe := remote
	unsafe.URL = "file:///etc/shadow"
	if err := ValidateRequest(request(OperationPeerSwapSourceWrite, unsafe, false)); err == nil {
		t.Fatal("unsafe PeerSwap source accepted")
	}
	paths := appmanifest.DefaultPeerSwapPaths()
	config := "host=127.0.0.1:42069\n" +
		"lnd.host=127.0.0.1:10009\n" +
		"lnd.tlscertpath=" + paths.LNDTLSCertPath + "\n" +
		"lnd.macaroonpath=" + paths.LNDMacaroonPath + "\n" +
		"elementsd.rpcuser=rpc\n" +
		"elementsd.rpcpass=secret\n" +
		"elementsd.rpchost=https://elements.example\n" +
		"elementsd.rpcport=7041\n" +
		"elementsd.rpcwallet=peerswap_node\n" +
		"elementsd.datadir=/media/liquid/elements\n" +
		"elementsd.liquidswaps=true\n" +
		"bitcoinswaps=false\n"
	webRaw, err := json.Marshal(map[string]any{
		"DataDir":           paths.RuntimeDir,
		"ElementsUser":      "rpc",
		"ElementsPass":      "secret",
		"BitcoinSwaps":      false,
		"Chain":             "mainnet",
		"ElementsDir":       "/media/liquid/elements",
		"ElementsDirMapped": "/media/liquid/elements",
		"ElementsHost":      "https://elements.example",
		"ElementsPort":      "7041",
		"ElementsWallet":    "peerswap_node",
		"LightningDir":      paths.LNDDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	ensure := PeerSwapEnsureParams{ElementsMode: appmanifest.PeerSwapElementsModeRemote, Config: config, WebConfig: string(webRaw), LNDTLSCertificate: []byte("cert")}
	if err := ValidateRequest(request(OperationPeerSwapEnsure, ensure, true)); err != nil {
		t.Fatalf("valid PeerSwap ensure rejected: %v", err)
	}
	ensure.Config = strings.Replace(ensure.Config, "lnd.macaroonpath="+paths.LNDMacaroonPath, "lnd.macaroonpath=/data/lnd/data/chain/bitcoin/mainnet/admin.macaroon", 1)
	if err := ValidateRequest(request(OperationPeerSwapEnsure, ensure, true)); err == nil {
		t.Fatal("PeerSwap admin macaroon path accepted")
	}
	if err := ValidateRequest(request(OperationPeerSwapLifecycle, PeerSwapLifecycleParams{Action: "exec"}, false)); err == nil {
		t.Fatal("arbitrary PeerSwap lifecycle action accepted")
	}
	if err := ValidateRequest(request(OperationPeerSwapSourceRead, struct{}{}, true)); err == nil {
		t.Fatal("PeerSwap source read dry-run accepted")
	}
}

func TestTapdRequestsAreTypedAndBounded(t *testing.T) {
	request := func(operation Operation, params any, dryRun bool) Request {
		raw, err := MarshalParams(params)
		if err != nil {
			t.Fatal(err)
		}
		return Request{Version: ProtocolVersion, RequestID: "tapd_test_1", Operation: operation, DryRun: dryRun, Params: raw}
	}
	ensure := TapdEnsureParams{
		DatabasePassword:  "0123456789abcdef0123456789abcdef",
		LNDTLSCertificate: []byte("certificate"),
		LNDMacaroon:       []byte("dedicated"),
	}
	if err := ValidateRequest(request(OperationTapdEnsure, ensure, true)); err != nil {
		t.Fatalf("valid Tapd ensure rejected: %v", err)
	}
	if err := ValidateRequest(request(OperationTapdLifecycle, TapdLifecycleParams{Action: AppLifecycleRestart}, false)); err == nil {
		t.Fatal("Tapd restart action was accepted")
	}
	if err := ValidateRequest(request(OperationTapdCLI, appmanifest.TapdCLIRequest{Command: appmanifest.TapdCLIGetInfo}, false)); err != nil {
		t.Fatalf("valid typed Tapd CLI rejected: %v", err)
	}
	if err := ValidateRequest(request(OperationTapdCLI, appmanifest.TapdCLIRequest{Command: "exec", Address: "tapbc1invalid"}, false)); err == nil {
		t.Fatal("arbitrary Tapd CLI command was accepted")
	}
	injected := Request{Version: ProtocolVersion, RequestID: "tapd_test_2", Operation: OperationTapdCLI,
		Params: json.RawMessage(`{"command":"get_info","args":[";reboot"]}`)}
	if err := ValidateRequest(injected); err == nil {
		t.Fatal("caller-selected Tapd argv was accepted")
	}
}

func TestDecodeRequestRejectsOversizedMessage(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), MaxMessageBytes+1)
	if _, err := DecodeRequest(bytes.NewReader(payload)); err == nil {
		t.Fatal("expected oversized request to fail")
	}
}

func TestPublicPoolRequestsAreTypedAndClosed(t *testing.T) {
	request := func(operation Operation, params any, dryRun bool) Request {
		raw, err := MarshalParams(params)
		if err != nil {
			t.Fatal(err)
		}
		return Request{Version: ProtocolVersion, RequestID: "publicpool_test", Operation: operation, DryRun: dryRun, Params: raw}
	}
	runtime := appmanifest.PublicPoolRuntime{BitcoinMode: appmanifest.PublicPoolBitcoinRemote, BitcoinRPCURL: "http://bitcoin.example", BitcoinRPCPort: 8332, BitcoinRPCUser: "rpcuser", BitcoinRPCPass: "rpcpass", BitcoinZMQHost: "tcp://bitcoin.example:28332"}
	if err := ValidateRequest(request(OperationPublicPoolEnsure, PublicPoolEnsureParams{Runtime: runtime}, true)); err != nil {
		t.Fatalf("valid ensure rejected: %v", err)
	}
	if err := ValidateRequest(request(OperationPublicPoolLifecycle, PublicPoolLifecycleParams{Action: AppLifecycleStart}, false)); err != nil {
		t.Fatalf("valid lifecycle rejected: %v", err)
	}
	if err := ValidateRequest(request(OperationPublicPoolLifecycle, PublicPoolLifecycleParams{Action: AppLifecycleRestart}, false)); err == nil {
		t.Fatal("restart accepted")
	}
	for _, raw := range []string{
		`{"runtime":{"bitcoin_mode":"remote","bitcoin_rpc_url":"http://bitcoin.example","bitcoin_rpc_port":8332,"bitcoin_rpc_user":"rpcuser","bitcoin_rpc_pass":"rpcpass","image":"evil/root:latest"}}`,
		`{"runtime":{"bitcoin_mode":"remote","bitcoin_rpc_url":"http://bitcoin.example","bitcoin_rpc_port":8332,"bitcoin_rpc_user":"rpcuser","bitcoin_rpc_pass":"bad$password"}}`,
		`{"path":"/etc/passwd"}`,
	} {
		req := Request{Version: ProtocolVersion, RequestID: "publicpool_test", Operation: OperationPublicPoolEnsure, Params: json.RawMessage(raw)}
		if err := ValidateRequest(req); err == nil {
			t.Fatalf("unsafe request accepted: %s", raw)
		}
	}
}

func TestValidateServiceUnitAllowlist(t *testing.T) {
	for _, unit := range []string{"lnd", "lnd@default", "lightningos-manager", "postgresql"} {
		if err := ValidateServiceUnit(unit); err != nil {
			t.Fatalf("expected %q to be allowed: %v", unit, err)
		}
	}
	for _, unit := range []string{"", " lnd", "lnd ", "lnd.service", "docker", "ssh", "lnd;reboot", "../lnd"} {
		if err := ValidateServiceUnit(unit); err == nil {
			t.Fatalf("expected %q to be rejected", unit)
		}
	}
}

func FuzzDecodeRequest(f *testing.F) {
	f.Add([]byte(`{"version":1,"request_id":"fuzz_1","operation":"self_test","params":{}}`))
	f.Add([]byte(`{"version":1,"request_id":"fuzz_2","operation":"service.restart","params":{"unit":"lnd"}}`))
	f.Add([]byte(`{"version":1,"request_id":"bad","operation":"/bin/sh","params":{"unit":"lnd;reboot"}}`))
	f.Fuzz(func(t *testing.T, payload []byte) {
		request, err := DecodeRequest(bytes.NewReader(payload))
		if err == nil {
			if err := ValidateRequest(request); err != nil {
				t.Fatalf("accepted request did not validate: %v", err)
			}
		}
	})
}

func TestBitcoinCoreStorageRequestUsesCanonicalClosedPath(t *testing.T) {
	valid := `{"version":1,"request_id":"bitcoin_storage_1","operation":"app.bitcoincore.storage.ensure","params":{"data_dir":"/mnt/bitcoin-ssd/bitcoin"}}`
	if _, err := DecodeRequest(strings.NewReader(valid)); err != nil {
		t.Fatalf("valid storage request rejected: %v", err)
	}
	for _, payload := range []string{
		`{"version":1,"request_id":"bitcoin_storage_1","operation":"app.bitcoincore.storage.ensure","params":{"data_dir":"/mnt/bitcoin/../bitcoin-data"}}`,
		`{"version":1,"request_id":"bitcoin_storage_1","operation":"app.bitcoincore.storage.ensure","params":{"data_dir":"/etc/bitcoin"}}`,
		`{"version":1,"request_id":"bitcoin_storage_1","operation":"app.bitcoincore.storage.ensure","params":{"data_dir":"/mnt/bitcoin","storage_id":"attacker"}}`,
	} {
		if _, err := DecodeRequest(strings.NewReader(payload)); err == nil {
			t.Fatalf("unsafe storage request accepted: %s", payload)
		}
	}
}

func TestBitcoinCoreConfigRequestsAreClosedAndSecretBounded(t *testing.T) {
	valid := []string{
		`{"version":1,"request_id":"bitcoin_config_1","operation":"app.bitcoincore.config.read","params":{"data_dir":"/mnt/bitcoin-ssd/bitcoin"}}`,
		`{"version":1,"request_id":"bitcoin_config_2","operation":"app.bitcoincore.config.ensure","params":{"data_dir":"/mnt/bitcoin-ssd/bitcoin","content":"server=1\n","generate_rpcauth":true}}`,
		`{"version":1,"request_id":"bitcoin_config_3","operation":"app.bitcoincore.config.write","dry_run":true,"params":{"data_dir":"/data/bitcoin","content":"server=1\n"}}`,
		`{"version":1,"request_id":"bitcoin_credentials_1","operation":"app.bitcoincore.credentials.read","params":{"data_dir":"/data/bitcoin"}}`,
	}
	for _, payload := range valid {
		if _, err := DecodeRequest(strings.NewReader(payload)); err != nil {
			t.Fatalf("valid bitcoin config request rejected: %v", err)
		}
	}

	invalid := []string{
		`{"version":1,"request_id":"bitcoin_config_4","operation":"app.bitcoincore.config.read","dry_run":true,"params":{"data_dir":"/data/bitcoin"}}`,
		`{"version":1,"request_id":"bitcoin_config_5","operation":"app.bitcoincore.config.read","params":{"data_dir":"/etc/bitcoin"}}`,
		`{"version":1,"request_id":"bitcoin_config_6","operation":"app.bitcoincore.config.ensure","params":{"data_dir":"/data/bitcoin","content":"server=1"}}`,
		`{"version":1,"request_id":"bitcoin_config_7","operation":"app.bitcoincore.config.write","params":{"data_dir":"/data/bitcoin","content":"server=1\r\n"}}`,
		`{"version":1,"request_id":"bitcoin_config_8","operation":"app.bitcoincore.config.write","params":{"data_dir":"/data/bitcoin","content":"server=1\n","path":"/etc/passwd"}}`,
		`{"version":1,"request_id":"bitcoin_config_9","operation":"app.bitcoincore.config.write","params":{"data_dir":"/data/bitcoin","content":"server=1\n","generate_rpcauth":true}}`,
		`{"version":1,"request_id":"bitcoin_credentials_2","operation":"app.bitcoincore.credentials.read","dry_run":true,"params":{"data_dir":"/data/bitcoin"}}`,
		`{"version":1,"request_id":"bitcoin_credentials_3","operation":"app.bitcoincore.credentials.read","params":{"data_dir":"/data/bitcoin","user":"attacker"}}`,
	}
	for _, payload := range invalid {
		if _, err := DecodeRequest(strings.NewReader(payload)); err == nil {
			t.Fatalf("unsafe bitcoin config request accepted: %s", payload)
		}
	}
}

func TestBitcoinCoreStatusRequestIsReadOnlyAndParameterless(t *testing.T) {
	valid := `{"version":1,"request_id":"bitcoin_status_1","operation":"app.bitcoincore.status","params":{}}`
	if _, err := DecodeRequest(strings.NewReader(valid)); err != nil {
		t.Fatalf("valid bitcoin status request rejected: %v", err)
	}
	for _, payload := range []string{
		`{"version":1,"request_id":"bitcoin_status_2","operation":"app.bitcoincore.status","dry_run":true,"params":{}}`,
		`{"version":1,"request_id":"bitcoin_status_3","operation":"app.bitcoincore.status","params":{"method":"stop"}}`,
		`{"version":1,"request_id":"bitcoin_status_4","operation":"app.bitcoincore.status","params":{"data_dir":"/etc"}}`,
	} {
		if _, err := DecodeRequest(strings.NewReader(payload)); err == nil {
			t.Fatalf("unsafe bitcoin status request accepted: %s", payload)
		}
	}
}
