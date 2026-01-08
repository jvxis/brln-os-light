# FILE: docs/09_SECURITY_MODEL.md

# Modelo de Segurança (v0.1)

## Acesso
- UI/API apenas em 127.0.0.1:8443 por padrão.
- Usuário acessa via LAN/VPN.
- Nada de reverse proxy ou WAN no MVP.

## Segredos
- rpcuser/rpcpass armazenados somente em /etc/lightningos/secrets.env (root:lightningos, 660) para permitir escrita pelo manager.
- DSN Postgres idem.
- UI nunca re-exibe segredos após salvar.

## Seed Phrase
- Nunca persistir seed.
- Para “Criar wallet”: exibir seed words 1 vez e exigir confirmação.
- Para “Importar”: capturar seed via UI e enviar para endpoint que chama InitWallet; não logar seed.

## Permissões
- `lnd` e `lightningos` são system users sem shell.
- Manager só precisa:
  - ler tls/macaroon do LND (preferível via grupo/ACL)
  - editar /etc/lnd/lnd.conf (via root? alternativa: manager roda sem root e usa helper via sudoers com comandos restritos)
Recomendação v0.1: rodar manager sem root + usar helper com sudoers restrito (v0.2). No MVP pode rodar com permissões ampliadas, mas documentar.

## Logs
- Redigir (redact) strings que pareçam senha/seed/DSN.
- Não logar request bodies de endpoints sensíveis.

