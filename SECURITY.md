# Security Policy

## Supported versions

Security fixes are developed for the current release line published as the
repository's latest GitHub Release. Older builds may receive best-effort upgrade
assistance, but are not guaranteed to receive backports. Before reporting a
problem, confirm the installed LightningOS version and whether the node was
originally installed with `install.sh`, `install_existing.sh`, or
`install_existing_pi.sh`.

LightningOS is intended for a trusted LAN or private VPN and is not designed for
direct public-Internet exposure. A report involving an Internet-exposed manager
is still welcome, but exposure should be removed immediately without publishing
credentials or node details.

## Reporting a vulnerability

Do not open a public Issue for vulnerabilities, exposed credentials, or situations that may put node funds at risk.

Use GitHub's [private vulnerability reporting](https://github.com/jvxis/brln-os-light/security/advisories/new). Include the affected LightningOS version, installation origin (`install.sh` or `install_existing.sh`), impact, reproduction steps, and a minimal proof of concept when safe.

Never submit wallet seeds, private keys, passwords, macaroons, cookies, tokens, RPC credentials, or production wallet backups. Redact public IP addresses and personal information unless they are essential to the report.

Please keep the report private until the maintainers have assessed the impact
and coordinated any fix or disclosure. Operational support questions and bugs
without a security impact may use the repository's public Issue forms after all
secrets and personal information have been removed.

## Versões suportadas

Correções de segurança são desenvolvidas para a linha atual publicada como a
release mais recente do repositório no GitHub. Versões anteriores podem receber
ajuda de upgrade em caráter de melhor esforço, mas não têm garantia de backport.
Antes de relatar um problema, confirme a versão instalada do LightningOS e se o
node foi originalmente instalado com `install.sh`, `install_existing.sh` ou
`install_existing_pi.sh`.

O LightningOS deve ser usado em uma LAN confiável ou VPN privada e não foi
projetado para exposição direta à Internet. Relatos envolvendo um Manager
exposto continuam sendo bem-vindos, mas a exposição deve ser removida
imediatamente sem publicar credenciais ou detalhes do node.

## Relatando uma vulnerabilidade

Não abra uma Issue pública para vulnerabilidades, credenciais expostas ou situações que possam colocar os fundos do node em risco.

Use o [relato privado de vulnerabilidades do GitHub](https://github.com/jvxis/brln-os-light/security/advisories/new). Informe a versão afetada do LightningOS, a origem da instalação (`install.sh` ou `install_existing.sh`), o impacto, os passos para reprodução e uma prova de conceito mínima quando for seguro.

Nunca envie seeds, chaves privadas, senhas, macaroons, cookies, tokens, credenciais RPC ou backups de carteiras de produção. Oculte endereços IP públicos e dados pessoais quando não forem essenciais para o relato.

Mantenha o relato privado até que os mantenedores avaliem o impacto e coordenem
uma eventual correção ou divulgação. Dúvidas operacionais e bugs sem impacto de
segurança podem usar os formulários públicos de Issues depois que todos os
segredos e dados pessoais forem removidos.
