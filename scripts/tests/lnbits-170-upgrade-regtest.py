"""Isolated LNbits database/image upgrade fixture, never a production updater.

Run as root on the disposable los-regtest host with Python requests and Docker.
Uses VoidWallet (no real funds/RPC), a unique retained data directory and only
the fixed test container below. No passwords, TOTP secrets or codes are printed.
"""
import argparse, base64, hashlib, hmac, json, os, pathlib, secrets, socket, sqlite3, struct, subprocess, tempfile, time
import requests

if os.geteuid()!=0 or socket.gethostname()!='los-regtest':
    raise SystemExit('Requires root on the isolated los-regtest host')
if pathlib.Path('/etc/los-regtest/LAB_ONLY').read_text().strip()!='LOS_REGTEST_ONLY':
    raise SystemExit('Disposable lab marker mismatch')
old='lnbits/lnbits:v1.6.2@sha256:284b9c2a0df9a1f867b4c52b694699fccade513c09368879922ff90bc7bf850e'
parser=argparse.ArgumentParser()
parser.add_argument('--from-170',action='store_true',help='Enroll TOTP on 1.7.0 before upgrading')
args=parser.parse_args()
if args.from_170:
    old='lnbits/lnbits:v1.7.0@sha256:9fc5157f31339968537beedc9517d22a91b5f2e171fcb42f1ca76f5305502df1'
new='lnbits/lnbits:v1.7.1@sha256:378fbe27aa5614f6fa05e7dcb541014654d1e3933fb9638e3955c146e0a28fa0'
name='los0555-lnbits170-upgrade'
if subprocess.check_output(['docker','ps','-aq','--filter','name=^'+name+'$']).strip():
    raise SystemExit('Test container already exists; preserve it and investigate')
root=pathlib.Path(tempfile.mkdtemp(prefix='lnbits170-upgrade-',dir='/var/lib/los-regtest'))
root.chmod(0o750);os.chown(root,65532,65532)
data=root/'data';data.mkdir(mode=0o750);os.chown(data,65532,65532)
env=root/'env'
env.write_text('LNBITS_BACKEND_WALLET_CLASS=VoidWallet\nLNBITS_DATA_FOLDER=/app/data\nLNBITS_EXTENSIONS_PATH=/app/data/extensions\nLNBITS_EXTENSIONS_DEFAULT_INSTALL=[]\nLNBITS_USER_DEFAULT_EXTENSIONS=[]\nLNBITS_TWO_FACTOR_ENABLED=true\nAUTH_HTTPS_ONLY=false\n')
env.chmod(0o600)
password=secrets.token_hex(24)
session=requests.Session()
def api(method,path,payload=None,status=200):
    r=session.request(method,'http://127.0.0.1:15000'+path,json=payload,timeout=30)
    assert r.status_code==status,(method,path,r.status_code)
    return r.json()
def login():
    session.cookies.clear()
    return api('POST','/api/v1/auth',{'username':'upgradetest','password':password})
def stop():
    subprocess.run(['docker','stop','--time','30',name],check=True,stdout=subprocess.DEVNULL)
    subprocess.run(['docker','rm',name],check=True,stdout=subprocess.DEVNULL)
def start(image):
    subprocess.run(['docker','run','-d','--name',name,'--user','65532:65532','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges:true','--network','host','--tmpfs','/tmp:rw,noexec,nosuid,nodev,size=64m,mode=1777','--env-file',str(env),'-e','HOME=/app/data','-e','PYTHONDONTWRITEBYTECODE=1','-v',str(data)+':/app/data:rw',image,'/app/.venv/bin/lnbits','--port','15000','--host','127.0.0.1'],check=True,stdout=subprocess.DEVNULL)
    for _ in range(120):
        try:
            if session.get('http://127.0.0.1:15000/',timeout=2).status_code==200:return
        except requests.RequestException:pass
        time.sleep(1)
    raise AssertionError('startup timeout')
try:
    subprocess.run(['docker','pull',old],check=True,stdout=subprocess.DEVNULL)
    start(old)
    api('PUT','/api/v1/auth/first_install',{'username':'upgradetest','password':password,'password_repeat':password})
    login()
    account=api('GET','/api/v1/auth')
    old_two_factor=api('GET','/admin/api/v1/settings').get('lnbits_two_factor_enabled')
    api('PUT','/admin/api/v1/settings',{'lnbits_site_title':'Preserved upgrade fixture'})
    if args.from_170:
        api('PUT','/admin/api/v1/settings',{'lnbits_two_factor_enabled':True})
        old_two_factor=True
        setup=api('POST','/api/v1/auth/2fa/setup')
        secret=base64.b32decode(setup['secret'])
        digest=hmac.new(secret,struct.pack('>Q',int(time.time())//30),hashlib.sha1).digest();offset=digest[-1]&15
        code=str((struct.unpack('>I',digest[offset:offset+4])[0]&0x7fffffff)%1000000).zfill(6)
        old_recovery=api('POST','/api/v1/auth/2fa/confirm',{'code':code})['recovery_codes']
        api('POST','/api/v1/auth/2fa/recovery/acknowledge')
        old_totp_key=hashlib.sha256((data/'.lnbits_totp_key').read_bytes()).hexdigest()
        assert login()['two_factor_required']
        api('POST','/api/v1/auth/2fa/verify',{'code':old_recovery[0]})
        api('GET','/api/v1/auth')
    stop()
    before={p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in data.iterdir() if p.name=='.lnbits_auth_key'}
    start(new)
    # Keep the old session cookie: the security release must reject it.
    api('GET','/api/v1/auth',status=401)
    auth_key=hashlib.sha256((data/'.lnbits_auth_key_v1').read_bytes()).hexdigest()
    assert before and auth_key not in before.values()
    result=login()
    if args.from_170:
        assert result['two_factor_required']
        assert hashlib.sha256((data/'.lnbits_totp_key').read_bytes()).hexdigest()==old_totp_key
        api('GET','/api/v1/auth',status=401)
        api('POST','/api/v1/auth/2fa/verify',{'code':old_recovery[0]},status=401)
        api('POST','/api/v1/auth/2fa/verify',{'code':old_recovery[1]})
        api('DELETE','/api/v1/auth/2fa')
    current=api('GET','/api/v1/auth')
    assert current['id']==account['id']
    assert [(w['id'],w['balance_msat']) for w in current['wallets']]==[(w['id'],w['balance_msat']) for w in account['wallets']]
    assert api('GET','/admin/api/v1/settings')['lnbits_site_title']=='Preserved upgrade fixture'
    assert api('GET','/admin/api/v1/settings')['lnbits_extensions_reviews_url']==''
    assert all(hashlib.sha256((data/p).read_bytes()).hexdigest()==v for p,v in before.items())
    state=api('GET','/api/v1/auth/2fa/status')
    if old_two_factor is not None: assert state['available']==old_two_factor
    assert not state['mandatory']
    if not state['available']:
        api('PUT','/admin/api/v1/settings',{'lnbits_two_factor_enabled':True})
        assert api('GET','/api/v1/auth/2fa/status')['available']
    with sqlite3.connect(data/'database.sqlite3') as db:
        assert db.execute('select count(*) from installed_extensions').fetchone()[0]==0
    setup=api('POST','/api/v1/auth/2fa/setup')
    secret=base64.b32decode(setup['secret'])
    digest=hmac.new(secret,struct.pack('>Q',int(time.time())//30),hashlib.sha1).digest();offset=digest[-1]&15
    code=str((struct.unpack('>I',digest[offset:offset+4])[0]&0x7fffffff)%1000000).zfill(6)
    recovery=api('POST','/api/v1/auth/2fa/confirm',{'code':code})['recovery_codes']
    api('POST','/api/v1/auth/2fa/recovery/acknowledge')
    totp_key=hashlib.sha256((data/'.lnbits_totp_key').read_bytes()).hexdigest()
    stop();start(new)
    assert hashlib.sha256((data/'.lnbits_auth_key_v1').read_bytes()).hexdigest()==auth_key
    assert hashlib.sha256((data/'.lnbits_totp_key').read_bytes()).hexdigest()==totp_key
    assert login()['two_factor_required']
    api('GET','/api/v1/auth',status=401)
    api('POST','/api/v1/auth/2fa/verify',{'code':recovery[0]})
    assert api('GET','/api/v1/auth')['id']==account['id']
    print('PASS', '1.7.0 (enrolled TOTP)' if args.from_170 else '1.6.2', '-> 1.7.1: account/wallet/balance/custom settings preserved; auth key rotated and old session rejected; reviews URL cleared; no extensions; TOTP enrollment and recovery persist across container replacement')
    print('Retained disposable data:',root)
finally:
    if subprocess.check_output(['docker','ps','-aq','--filter','name=^'+name+'$']).strip():stop()
