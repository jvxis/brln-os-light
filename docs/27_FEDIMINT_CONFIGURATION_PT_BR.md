# Configuracao do Fedimint no LightningOS Light

Este guia documenta a integracao atual do app Fedimint na App Store do LightningOS Light.

O app instala dois servicos Docker:

- `fedimintd`: guardiao da federacao.
- `gatewayd lnd`: gateway Lightning conectado ao LND local do LightningOS.

Ele nao instala um `bitcoind` proprio. O Fedimint reutiliza o Bitcoin RPC ja configurado no LightningOS, seja o Bitcoin Core da App Store, um Bitcoin local externo ou um Bitcoin remoto.

## Pre-requisitos

- Bitcoin RPC configurado no LightningOS.
- LND local funcionando e sincronizado.
- Arquivos do LND disponiveis em:
  - `/data/lnd/tls.cert`
  - `/data/lnd/data/chain/bitcoin/mainnet/admin.macaroon`
- Docker disponivel. A App Store instala Docker sob demanda quando necessario.

## Endpoints

Em uma instalacao LAN, substitua `<SERVER_LAN_IP>` pelo IP do servidor LightningOS:

- Guardian UI: `http://<SERVER_LAN_IP>:8175`
- Gateway UI: `http://<SERVER_LAN_IP>:8176`
- Gateway Lightning V2 URL: `http://<SERVER_LAN_IP>:8176/v1`

O endpoint `http://<SERVER_LAN_IP>:8176/v1` serve apenas para testes dentro da mesma rede. Para usuarios externos, registre o gateway com uma URL publica HTTPS, por exemplo:

```text
https://gateway.exemplo.com/v1
```

Nao registre `192.168.x.x`, `10.x.x.x` ou `172.16-31.x.x` para uma federacao que sera usada fora da LAN.

## Portas usadas

- `8173/tcp` e `8173/udp`: comunicacao P2P do guardiao.
- `8174/udp`: API/Iroh do guardiao.
- `8175/tcp`: Guardian UI.
- `8176/tcp`: Gateway UI/API.
- `8177/udp`: Iroh do gateway.

Como o gateway roda em modo LND (`gatewayd lnd`), a porta Lightning `10010` do modo LDK nao e usada por esta integracao.

## LND

O gateway usa o LND local via gRPC:

```text
https://host.docker.internal:10009
```

Durante a instalacao/start do Fedimint, o LightningOS pode adicionar entradas ao `/data/lnd/lnd.conf` para permitir acesso gRPC a partir da rede Docker do app:

```text
tlsextradomain=host.docker.internal
rpclisten=127.0.0.1:10009
tlsextraip=<docker-gateway-ip>
rpclisten=<docker-gateway-ip>:10009
```

Essa alteracao e aditiva. Se o arquivo mudar, o LightningOS remove `tls.cert`/`tls.key` para o LND regenerar o certificado e reinicia o servico `lnd`.

## Bitcoin

O app resolve o Bitcoin RPC a partir da configuracao existente:

- Bitcoin Core da App Store: usa a rede Docker `bitcoincore_default` e acessa `http://bitcoind:8332`.
- Bitcoin local externo: usa `host.docker.internal:<porta>`.
- Bitcoin remoto: usa as credenciais ja configuradas no LightningOS/LND.

As credenciais sao gravadas no arquivo de ambiente do app em:

```text
/var/lib/lightningos/apps/fedimint/.env
```

## Caminhos de dados

- Compose: `/var/lib/lightningos/apps/fedimint/docker-compose.yaml`
- Env: `/var/lib/lightningos/apps/fedimint/.env`
- Dados do guardiao: `/var/lib/lightningos/apps-data/fedimint/fedimintd`
- Dados do gateway: `/var/lib/lightningos/apps-data/fedimint/gatewayd`
- Senha admin do gateway: `/var/lib/lightningos/apps-data/fedimint/gateway-admin.txt`

Ao desinstalar pela App Store, o LightningOS remove os arquivos do app em `/var/lib/lightningos/apps/fedimint`, mas preserva os dados persistentes em `/var/lib/lightningos/apps-data/fedimint`.

## Desinstalacao e reset completo

O botao `Uninstall` da App Store e conservador. Ele executa `docker compose down --remove-orphans` e remove os arquivos operacionais em:

```text
/var/lib/lightningos/apps/fedimint
```

Ele nao remove automaticamente:

- dados do guardiao em `/var/lib/lightningos/apps-data/fedimint/fedimintd`;
- dados do gateway em `/var/lib/lightningos/apps-data/fedimint/gatewayd`;
- senha admin do gateway;
- imagens Docker baixadas;
- entradas adicionadas ao `/data/lnd/lnd.conf`;
- regras UFW adicionadas.

Isso evita apagar uma federacao por acidente. Em uma federacao real, remover os dados do guardiao pode causar perda operacional ou perda de acesso se nao houver backup.

Para uma instalacao de teste ou uma instalacao parcial/quebrada, use reset completo apenas quando tiver certeza de que os dados podem ser descartados:

```bash
sudo docker compose \
  --env-file /var/lib/lightningos/apps/fedimint/.env \
  --project-directory /var/lib/lightningos/apps/fedimint \
  -f /var/lib/lightningos/apps/fedimint/docker-compose.yaml \
  down --remove-orphans

sudo rm -rf /var/lib/lightningos/apps/fedimint
sudo rm -rf /var/lib/lightningos/apps-data/fedimint
```

Depois atualize/reinstale o LightningOS Manager e instale o Fedimint novamente pela App Store:

```bash
cd ~/brln-os-light
git pull
cd lightningos-light
sudo ./install_existing.sh
sudo systemctl restart lightningos-manager
```

## Criar federacao de teste com 1 guardiao

1. Instale o Fedimint pela App Store.
2. Abra a Guardian UI em `http://<SERVER_LAN_IP>:8175`.
3. Crie uma nova federacao solo apenas para teste.
4. Baixe o backup do guardiao.
5. Abra a Gateway UI em `http://<SERVER_LAN_IP>:8176`.
6. Confirme que o LND aparece como `External LND` e `Synced`.
7. Cole o invite code da federacao em `Connect a new Federation`.
8. Na Guardian UI, em Lightning V2, registre o gateway:

```text
http://<SERVER_LAN_IP>:8176/v1
```

Use uma URL publica HTTPS no lugar do IP LAN se clientes externos forem participar.

## Criar federacao basica com 4 guardioes

Uma federacao solo existente nao deve ser convertida para 4 guardioes. Crie uma nova federacao.

Modelo recomendado para teste:

- Guardiao 1: seu servidor LightningOS, com `fedimintd` e `gatewayd lnd`.
- Guardiao 2: servidor do usuario A, com `fedimintd`.
- Guardiao 3: servidor do usuario B, com `fedimintd`.
- Guardiao 4: servidor do usuario C, com `fedimintd`.

Somente o servidor que opera o gateway precisa conectar ao LND. Os outros guardioes nao precisam acessar o seu LND.

Fluxo operacional:

1. Cada guardiao instala o app Fedimint em seu proprio servidor.
2. Cada guardiao abre sua Guardian UI.
3. Um guardiao inicia o setup da federacao com 4 guardioes.
4. Cada guardiao gera seu setup code.
5. Os 4 guardioes compartilham os setup codes por um canal seguro fora da aplicacao.
6. Cada guardiao informa os codigos dos demais na Guardian UI.
7. Todos finalizam o setup/DKG.
8. Cada guardiao baixa e guarda seu backup.
9. O operador do gateway cola o invite code da federacao na Gateway UI.
10. A federacao registra o gateway em Lightning V2 com uma URL acessivel pelos clientes.

Com `FM_ENABLE_IROH=true`, a comunicacao entre guardioes pode funcionar mesmo atras de NAT/firewall. Para producao, prefira rede estavel e endpoint publico HTTPS para o gateway.

## Verificacoes

Status dos containers:

```bash
sudo docker compose -f /var/lib/lightningos/apps/fedimint/docker-compose.yaml ps
```

Logs:

```bash
sudo docker compose -f /var/lib/lightningos/apps/fedimint/docker-compose.yaml logs --tail=200 fedimintd
sudo docker compose -f /var/lib/lightningos/apps/fedimint/docker-compose.yaml logs --tail=200 gatewayd
```

Checar se o LND foi preparado para Docker:

```bash
sudo grep -E 'host.docker.internal|rpclisten=.*10009|tlsextraip' /data/lnd/lnd.conf
```

Na Gateway UI, confira:

- `Gateway Network Information`: `Running`.
- `Lightning Node`: `External LND`.
- `Status`: `Synced`.
- `Block Height`: proximo ao bloco atual do LND.

Na Guardian UI, confira:

- Bitcoin RPC conectado.
- Lightning V2 com o gateway registrado.
- Wallet sem erros de consenso.

## Troubleshooting: LND nao inicia apos reset/desinstalacao

Se o LND falhar com erro parecido com:

```text
listen tcp4 172.19.0.1:10009: bind: cannot assign requested address
```

isso indica que ficou uma entrada `rpclisten` apontando para uma bridge Docker que nao existe mais. Isso pode acontecer depois de uma instalacao parcial, reset manual ou remocao de rede Docker.

Confirme a linha no `lnd.conf`:

```bash
sudo grep -nE 'rpclisten=.*10009|tlsextraip=' /data/lnd/lnd.conf
ip -4 addr | grep 172.19.0.1
```

Se o IP do erro nao aparecer no `ip -4 addr`, remova apenas as linhas daquele IP:

```bash
sudo cp /data/lnd/lnd.conf /data/lnd/lnd.conf.bak-fedimint-$(date +%Y%m%d-%H%M%S)
sudo sed -i '/^rpclisten=172\.19\.0\.1:10009$/d' /data/lnd/lnd.conf
sudo sed -i '/^tlsextraip=172\.19\.0\.1$/d' /data/lnd/lnd.conf
sudo rm -f /data/lnd/tls.cert /data/lnd/tls.key
sudo systemctl restart lnd
sudo journalctl -u lnd -n 80 --no-pager
```

Troque `172.19.0.1` pelo IP exato mostrado no erro. Nao remova `rpclisten=127.0.0.1:10009`.

## Observacoes de seguranca

- Federacao com 1 guardiao e apenas para teste.
- Para producao, use varios guardioes independentes. Uma federacao com 4 guardioes tolera 1 guardiao offline/malicioso dentro do modelo BFT do Fedimint.
- Cada guardiao deve guardar seu backup.
- Nao compartilhe macaroon, senha do gateway ou arquivos de dados entre guardioes.
- O LND do operador do gateway nao deve ser exposto diretamente aos demais guardioes.
