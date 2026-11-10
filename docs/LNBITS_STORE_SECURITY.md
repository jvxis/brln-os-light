# LNbits in the LightningOS App Store / LNbits na loja

## English

Starting with the 0.5.55 candidate, the Store pins LNbits **1.7.0** by its
official multi-architecture image digest. After upgrading LOS, stop LNbits in
the Store and start it again to apply the catalog version. Keep a backup of
the LNbits data directory before an application/database upgrade; changing an
image back is not a supported database rollback.

- A new installation starts with **no extensions installed or enabled for
  users**. Automatic startup installation is explicitly disabled. Administrators
  may deliberately install extensions; install only code you trust.
- Upgrading does **not** uninstall existing extensions, delete their data or
  reset account settings. Previously selected user-default extensions are
  preserved. Disabling automatic installation is not a blanket extension ban.
- **2FA is available by default**, not automatically enrolled or mandatory.
  In LNbits, open your account's two-factor authentication settings, scan the
  QR with your authenticator, confirm the code and save the recovery codes
  securely. Until you finish enrollment, your account is not protected by 2FA.
- Existing explicit 2FA settings are preserved, including disabled or mandatory
  policies. An administrator can enable the feature in LNbits if it had been
  disabled. LOS does not generate a user's TOTP secret or store recovery codes.
- LNbits manages `.lnbits_totp_key` inside its persistent data directory. Back
  up the **whole directory**, including hidden files and database, privately.
  Do not delete/rotate this key to fix login problems or publish it in logs.
- The dedicated **wallet-only LND macaroon** remains restricted to information,
  invoices and Lightning payments. Node management, channel open/close,
  on-chain LND sends and peer management are not supported. Payments made by
  LNbits/extensions still bypass LOS spending guards. 2FA protects account
  authentication; it does not turn API keys into read-only credentials or
  make arbitrary extensions safe.

The Store warning and install confirmation explain these boundaries. No LND
restart, TLS regeneration or production-node operation is part of this change.

## Português

A partir da candidata 0.5.55, a loja fixa o LNbits **1.7.0** pelo digest da
imagem oficial multi-arquitetura. Depois de atualizar o LOS, pare o LNbits na
loja e inicie novamente para aplicar a versão do catálogo. Antes de atualizar
o aplicativo/banco de dados, faça backup dos dados do LNbits; voltar a imagem
anterior não constitui um rollback seguro do banco.

- Uma instalação nova começa **sem extensões instaladas ou habilitadas para
  usuários**. A instalação automática no início está explicitamente desativada.
  O administrador ainda pode instalar extensões por decisão própria: instale
  apenas código em que confia.
- O upgrade **não** desinstala extensões existentes, apaga seus dados ou
  redefine contas. Extensões padrão anteriormente escolhidas pelo operador
  são preservadas. Não se trata de proibir todas as extensões.
- O **2FA vem disponível**, mas não cadastrado nem obrigatório. Na sua conta
  LNbits, abra as configurações de autenticação em dois fatores, escaneie o QR
  no autenticador, confirme o código e guarde os códigos de recuperação em
  local seguro. A conta só fica protegida após concluir esse cadastro.
- Escolhas explícitas anteriores de 2FA são preservadas, inclusive políticas
  desabilitadas ou obrigatórias. Se necessário, o administrador pode habilitar
  o recurso no LNbits. O LOS não gera o segredo TOTP do usuário nem armazena
  seus códigos de recuperação.
- O próprio LNbits mantém `.lnbits_totp_key` no diretório persistente de dados.
  Faça backup privado do **diretório completo**, incluindo arquivos ocultos e
  banco. Não apague/troque essa chave para corrigir login e nunca a publique.
- O **macaroon LND limitado à carteira** continua permitindo informações,
  faturas e pagamentos Lightning. Gerenciamento do nó, abrir/fechar canais,
  envios on-chain pelo LND e gestão de peers não são suportados. Pagamentos
  feitos pelo LNbits/extensões continuam fora dos limites de gastos do LOS.
  2FA protege a autenticação da conta; não torna as chaves de API somente
  leitura nem torna extensões arbitrárias seguras.

Esses limites aparecem no card da loja e na confirmação de instalação. Esta
alteração não reinicia LND, regenera TLS nem modifica nodes de produção.

## Upstream references

- [LNbits v1.7.0](https://github.com/lnbits/lnbits/releases/tag/v1.7.0)
- [Settings and persistent TOTP key](https://github.com/lnbits/lnbits/blob/v1.7.0/lnbits/settings.py)
- [2FA API](https://github.com/lnbits/lnbits/blob/v1.7.0/lnbits/core/views/two_factor_api.py)
