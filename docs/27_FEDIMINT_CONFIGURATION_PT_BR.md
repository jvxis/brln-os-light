# Configuracao Fedimint no LightningOS

O LightningOS separa Fedimint em dois apps independentes:

- **Fedimint Guardian**: roda `fedimintd` e participa da federacao como guardiao.
- **Fedimint Lightning Gateway**: roda `gatewayd lnd` e conecta o LND local a uma ou mais federacoes.

Essa separacao segue a arquitetura do Fedimint: o Lightning Gateway nao e guardiao. Ele e um ator economico separado, usado para trocar ecash por pagamentos Lightning.

Referencias upstream:

- Deploy Fedimint: <https://github.com/fedimint/fedimint/blob/master/docs/deploying.md>
- Docker guardian: <https://github.com/fedimint/fedimint/tree/master/docker/fedimintd>
- Docker gateway: <https://github.com/fedimint/fedimint/tree/master/docker/gatewayd>
- Start9 Fedimint: <https://github.com/fedimint/fedimint/tree/master/fedimint-startos>

## Apps

### Fedimint Guardian

Use este app quando o usuario sera guardiao da federacao.

Ele roda apenas `fedimintd`.

Nao usa LND.

Configuracao principal:

```yaml
FM_ENABLE_IROH: "true"
FM_BITCOIN_NETWORK: bitcoin
FM_ESPLORA_URL: https://mempool.space/api
FM_BIND_P2P: 0.0.0.0:8173
FM_BIND_API: 0.0.0.0:8174
FM_BIND_UI: 0.0.0.0:8175
```

### Fedimint Lightning Gateway

Use este app no servidor que tem LND e quer oferecer gateway Lightning para uma federacao.

Ele roda apenas `gatewayd lnd`.

Configuracao principal:

```yaml
FM_GATEWAY_DATA_DIR: /data
FM_GATEWAY_LISTEN_ADDR: 0.0.0.0:8176
FM_GATEWAY_NETWORK: bitcoin
FM_GATEWAY_IROH_LISTEN_ADDR: 0.0.0.0:8177
FM_GATEWAY_BCRYPT_PASSWORD_HASH: <gerado pelo LightningOS>
FM_ESPLORA_URL: https://mempool.space/api
FM_LND_RPC_ADDR: https://host.docker.internal:10009
FM_LND_TLS_CERT: /data/lnd/tls.cert
FM_LND_MACAROON: /data/lnd/data/chain/bitcoin/mainnet/admin.macaroon
```

O gateway monta `/data/lnd` como somente leitura.

## Esplora

Os dois apps usam:

```text
https://mempool.space/api
```

Isso evita depender do Bitcoin RPC local dentro da integracao Fedimint.

Tradeoff: essa opcao e mais simples, mas menos privada e menos autonoma do que usar o proprio bitcoind. Se `mempool.space` ficar indisponivel, limitado ou instavel, o Fedimint pode ser afetado.

## Portas

### Guardian

- `8173/tcp`: P2P TLS.
- `8173/udp`: P2P Iroh.
- `8174/udp`: API Iroh.
- `8175/tcp`: Guardian UI.

### Gateway

- `8176/tcp`: Gateway UI/API.
- `8177/udp`: Gateway Iroh.

A porta `10010/tcp` nao e usada nesta integracao, porque ela pertence ao modo LDK. O LightningOS usa `gatewayd lnd`.

Nao exponha publicamente:

- LND gRPC `10009`;
- LND REST `8080`;
- Guardian UI `8175`, exceto por rede confiavel, VPN ou proxy autenticado.

## Iroh e IP publico

Com Iroh habilitado, nao exigimos dominio ou IP publico para criar uma federacao basica. Iroh tenta conexao direta e pode usar mecanismos de NAT traversal.

Abrir UDP ajuda a confiabilidade, mas nao deve ser tratado como pre-requisito absoluto para testes.

Para producao, ainda prefira rede estavel, backups e operadores independentes.

## Diretorios

### Guardian

- Compose: `/var/lib/lightningos/apps/fedimint-guardian/docker-compose.yaml`
- Dados: `/var/lib/lightningos/apps-data/fedimint-guardian/fedimintd`

### Gateway

- Compose: `/var/lib/lightningos/apps/fedimint-gateway/docker-compose.yaml`
- Dados: `/var/lib/lightningos/apps-data/fedimint-gateway/gatewayd`
- Senha admin: `/var/lib/lightningos/apps-data/fedimint-gateway/gateway-admin.txt`
- Hash da senha: `/var/lib/lightningos/apps-data/fedimint-gateway/gateway-password-hash.txt`

## Instalar uma federacao com 4 guardioes

Exemplo com voce e mais 3 usuarios do app:

1. Cada um dos 4 usuarios instala **Fedimint Guardian** no seu proprio servidor.
2. Cada guardiao abre a Guardian UI em `http://<IP_LAN>:8175`.
3. Cada guardiao define uma senha forte.
4. Cada guardiao gera seu setup code.
5. Todos trocam os setup codes entre si.
6. Cada guardiao insere os outros 3 setup codes na sua UI.
7. A federacao executa DKG.
8. Depois que a federacao estiver criada, gere o invite code.
9. O operador do LND instala **Fedimint Lightning Gateway**.
10. Na Gateway UI em `http://<IP_LAN>:8176`, cole o invite code em "Connect a new Federation".

O operador do gateway pode ser tambem um guardiao, mas nao precisa ser.

## Teste solo

Para teste local:

1. Instale **Fedimint Guardian**.
2. Crie uma federacao com 1 guardiao.
3. Instale **Fedimint Lightning Gateway** no mesmo servidor.
4. Abra `http://<IP_LAN>:8176`.
5. Confirme que o status do gateway esta `Running`.
6. Confirme que a secao Lightning Node mostra:
   - Node Type: `External LND`;
   - Status: `Synced`;
   - Block Height maior que zero.
7. Conecte o gateway a federacao usando o invite code.

## Validacoes

Guardian:

```bash
sudo docker compose \
  --project-directory /var/lib/lightningos/apps/fedimint-guardian \
  -f /var/lib/lightningos/apps/fedimint-guardian/docker-compose.yaml \
  ps

sudo docker compose \
  --project-directory /var/lib/lightningos/apps/fedimint-guardian \
  -f /var/lib/lightningos/apps/fedimint-guardian/docker-compose.yaml \
  logs --tail=200 fedimintd
```

Gateway:

```bash
sudo docker compose \
  --project-directory /var/lib/lightningos/apps/fedimint-gateway \
  -f /var/lib/lightningos/apps/fedimint-gateway/docker-compose.yaml \
  ps

sudo docker compose \
  --project-directory /var/lib/lightningos/apps/fedimint-gateway \
  -f /var/lib/lightningos/apps/fedimint-gateway/docker-compose.yaml \
  logs --tail=200 gatewayd
```

LND:

```bash
sudo grep -nE 'host.docker.internal|rpclisten=.*10009|tlsextraip=' /data/lnd/lnd.conf
sudo journalctl -u lnd -n 80 --no-pager
```

## Uninstall

O uninstall dos dois apps e destrutivo por design.

Ao desinstalar **Fedimint Guardian**, o LightningOS:

- para o compose;
- remove containers/orphans/volumes do compose;
- remove `/var/lib/lightningos/apps/fedimint-guardian`;
- remove `/var/lib/lightningos/apps-data/fedimint-guardian`;
- remove a imagem `fedimint/fedimintd:v0.11.1`.

Ao desinstalar **Fedimint Lightning Gateway**, o LightningOS:

- para o compose;
- remove containers/orphans/volumes do compose;
- remove `/var/lib/lightningos/apps/fedimint-gateway`;
- remove `/var/lib/lightningos/apps-data/fedimint-gateway`;
- remove a imagem `fedimint/gatewayd:v0.11.1`;
- nao altera o `lnd.conf`.

Antes de desinstalar um guardiao real, faca backup pela Guardian UI. Remover os dados do guardiao remove a identidade e o material local daquele guardiao.

## Migracao do app antigo combinado

Versoes anteriores do LightningOS usavam um unico app `fedimint`, com `fedimintd` e `gatewayd lnd` juntos.

Ao instalar os novos apps, o LightningOS tenta migrar de forma conservadora:

- instalar **Fedimint Guardian** move `/var/lib/lightningos/apps-data/fedimint/fedimintd` para o novo diretorio do Guardian;
- instalar **Fedimint Lightning Gateway** move `/var/lib/lightningos/apps-data/fedimint/gatewayd` e os arquivos de senha para o novo diretorio do Gateway;
- se o compose antigo existir, ele e parado antes da migracao.

Se os novos diretorios ja existirem, a migracao automatica nao sobrescreve esses dados.

## Problema comum: LND nao inicia com IP Docker antigo

Erro tipico:

```text
listen tcp4 172.19.0.1:10009: bind: cannot assign requested address
```

Isso indica `rpclisten` antigo no `lnd.conf`, apontando para uma bridge Docker que nao existe mais.

Comandos de reparo:

```bash
sudo cp /data/lnd/lnd.conf /data/lnd/lnd.conf.bak-fedimint-$(date +%Y%m%d-%H%M%S)
sudo sed -i '/^rpclisten=172\.19\.0\.1:10009$/d' /data/lnd/lnd.conf
sudo sed -i '/^tlsextraip=172\.19\.0\.1$/d' /data/lnd/lnd.conf
sudo rm -f /data/lnd/tls.cert /data/lnd/tls.key
sudo systemctl restart lnd
sudo journalctl -u lnd -n 80 --no-pager
```

Nao remova `rpclisten=127.0.0.1:10009`.
