# Optional recovery: Go preparation blocked by a legacy directory owner

## English

This is a **manual, opt-in support procedure**, not an installer prerequisite or
an automatic migration. Use it only when an upgrade from an installed
**0.5.40/0.5.41-Beta** stops at `Preparing the release Go toolchain` with
`Go preparation failed; application was not replaced.` Other failures require
their own diagnosis. If upgrades work, do nothing.

Issue [#230](https://github.com/jvxis/brln-os-light/issues/230) reports
`/opt/lightningos` owned by `admin:admin`, with mode `755`. This state can block
compiler preparation. It is **not established that install_existing.sh created
this owner**, and installation type alone is not a reason to run a repair.

The standalone [recovery script](../scripts/recover-go-parent-owner.py) requires
Linux, Python 3 and systemd. It checks by default; `--apply` explicitly authorizes
one owner/group change to the directory `/opt/lightningos` only. It never changes
descendants, permissions, LND, Bitcoin, certificates, configuration, or services;
it does not install Go, download anything or initiate an upgrade.

Validated on Ubuntu 24 amd64, including recovery followed by the actual installed
updater from 0.5.40 to 0.5.41. Other platforms have not been validated for this
helper; seek review before use. See the [test record](validation/go-parent-owner-recovery-0.5.42.md).

Safety checks require trusted root-owned ancestors, no symbolic links, no
extended POSIX ACLs, no mount point at the target, mode `0755`, an expected
Manager/UI layout and an idle updater. Repair is limited to the local `admin`
account and its primary group. An already `root:root` directory is a no-op.
Any different layout needs individual review, not recursive `chown`.

### Procedure

Wait for the failed upgrade to finish. Do not run concurrent installers,
upgrades or permission changes. Review the downloaded script before root execution.
Use the commit-pinned download and checksum published with this procedure, not a
moving branch or `curl | sudo bash`.

Download into a new temporary directory; stop unless checksum verification says
`OK`. Keep this terminal open (the next commands use `recovery_dir`).

```bash
recovery_dir=$(mktemp -d /tmp/los-go-owner.XXXXXX) &&
curl --fail --location --proto '=https' --tlsv1.2 \
  'https://raw.githubusercontent.com/jvxis/brln-os-light/f541bf35c121311f72bc3bb2d2e9db54d7e21b31/scripts/recover-go-parent-owner.py' \
  --output "$recovery_dir/recover-go-parent-owner.py" &&
printf '7b1b3624b6663116f76aad2ce2222f6dbd262638fb7aed0aa31d3316865e2683  %s\n' \
  "$recovery_dir/recover-go-parent-owner.py" | sha256sum --check -
```

Review the downloaded file. Then diagnose (no mutation):

```bash
test -n "${recovery_dir:-}" && sudo python3 -I "$recovery_dir/recover-go-parent-owner.py" --check
```

Only for `[ACTION REQUIRED] Known legacy owner detected`, explicitly apply:

```bash
test -n "${recovery_dir:-}" && sudo python3 -I "$recovery_dir/recover-go-parent-owner.py" --apply
```

The checksum verifies this exact reviewed script, not the authenticity of an
untrusted forwarded message. Obtain this guide from the official repository.
Never continue after a download/checksum failure. If using an already checked-out
script instead, the equivalent commands below run from its directory.

1. Run `sudo python3 -I recover-go-parent-owner.py --check` on the affected node.
2. Only if it prints `[ACTION REQUIRED] Known legacy owner detected`, run
   `sudo python3 -I recover-go-parent-owner.py --apply` using the same file.
3. Save the `[BEFORE]` and `[OK]` lines for support. They contain numeric ownership
   and mode, not credentials. Run `--check` again, then retry the upgrade yourself
   in the UI. Successful ownership recovery is **not** proof the upgrade completed.
4. If it prints `[REFUSED]`, stop and share only the diagnostic and upgrade error
   in the issue. Do not force the operation, change descendants or paste secrets.

Exit codes: `0` healthy/repaired, `2` known owner found in read-only check mode,
`1` refused/failed, including an unrecognized layout. Invalid CLI syntax also
exits `2` but prints usage, not `[ACTION REQUIRED]`.

There is no automatic rollback: reversing the parent owner reintroduces the
upgrade blocker. If a repair needs reversal, use the recorded numeric UID/GID
with maintainer assistance after inspecting the node; never recursively change
ownership. The helper preserves permissions and descendant state, not a backup
of the installation. It is not a general filesystem security audit.

## Português

Este é um procedimento **manual e opcional de suporte**, não um pré-requisito
para todos os usuários nem uma migração automática. Use somente se o node ainda
estiver na **0.5.40/0.5.41-Beta** e o upgrade parar em
`Preparing the release Go toolchain`, com
`Go preparation failed; application was not replaced.` Se o upgrade funciona,
nenhuma ação é necessária.

No issue #230, `/opt/lightningos` pertencia a `admin:admin`, com modo `755`.
Ainda não está comprovado em que etapa esse estado surgiu. Ter usado
`install_existing.sh`, por si só, **não significa precisar da correção**.

O script só altera o proprietário/grupo do próprio diretório
`/opt/lightningos`, para `root:root`, mediante `--apply`. Não é recursivo, não
muda permissões nem conteúdo dos descendentes, não altera LND/Bitcoin/TLS,
não reinicia serviços e não inicia o upgrade. Requer Linux, Python 3 e systemd.
Validado em Ubuntu 24 amd64, incluindo recuperação e upgrade real da 0.5.40 para
a 0.5.41. Outras plataformas ainda não foram validadas para este script.

Ele recusa links simbólicos, ACLs estendidas, ponto de montagem no destino,
ancestrais inseguros, modo diferente de `0755`, versão/layout desconhecido,
proprietário diferente do usuário local `admin` e seu grupo primário, ou updater
em andamento/estado indeterminado. Um diretório já `root:root` não é alterado.

### Como usar

Espere a tentativa de upgrade encerrar e não execute outras instalações ou
mudanças de permissões em paralelo. Use o download fixado em commit e o checksum
acima; revise o arquivo antes de executá-lo como root. Não use `curl | sudo bash`.
O bloco acima baixa em diretório temporário e exige checksum `OK`. Mantenha o
mesmo terminal aberto e use os comandos com `$recovery_dir` mostrados acima.
Os comandos curtos abaixo são equivalentes se você estiver na pasta do script.

1. Diagnóstico: `sudo python3 -I recover-go-parent-owner.py --check`.
2. **Somente se aparecer `[ACTION REQUIRED] Known legacy owner detected`**, execute
   `sudo python3 -I recover-go-parent-owner.py --apply` com o mesmo arquivo.
3. Guarde as linhas `[BEFORE]` e `[OK]`. Faça o `--check` novamente e tente o
   upgrade pela UI. Corrigir o diretório não significa que o upgrade terminou.
4. Se aparecer `[REFUSED]`, pare e envie apenas o diagnóstico e o erro do upgrade
   no issue. Não force a correção, não use `chown -R` e não envie credenciais.

O diagnóstico não modifica arquivos. Código de saída `0`: saudável/corrigido;
`2` com `[ACTION REQUIRED]`: caso conhecido encontrado; `1`: recusado/falhou.
Não há rollback automático: restaurar o proprietário anterior faria o bloqueio
voltar. Eventual reversão exige revisão do node e os UID/GID registrados,
sempre sem recursão.

### Texto sugerido para release/Telegram

> Upgrade bloqueado na preparação do Go? Algumas instalações antigas podem ter
> `/opt/lightningos` com proprietário incompatível. Disponibilizamos um
> procedimento opcional que primeiro diagnostica e só corrige o caso conhecido
> por confirmação explícita. Não altera LND/Bitcoin nem reinicia serviços.
> Se seu upgrade funciona, não precisa executar nada. Consulte este guia antes
> de aplicar qualquer correção.
