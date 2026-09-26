#!/usr/bin/env python3
"""Run a real reference-image contract check with disposable, untrusted test keys.

This never registers an RP, sends a message, or tests an actual wallet credential.
Run on a Linux host. Requires Docker, OpenSSL and Python's standard library.
"""
import os
import base64
import json
import hashlib
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import urllib.request
import urllib.error
import urllib.parse

ROOT = Path(__file__).resolve().parent.parent
IMAGE = 'ghcr.io/eu-digital-identity-wallet/eudi-srv-verifier-endpoint@sha256:cb2eddbb44fe2eabfed414270f3bbf4211a4cf71247ba2f884425b78839dec50'
GO = 'golang:1.26.4-bookworm@sha256:b305420a68d0f229d91eb3b3ed9e519fcf2cf5461da4bef997bf927e8c0bfd2b'
CLIENT_ID_PREFIX = os.environ.get('REFERENCE_CLIENT_ID_PREFIX', 'x509_san_dns')
if CLIENT_ID_PREFIX not in ('x509_san_dns', 'x509_hash'):
    raise SystemExit('REFERENCE_CLIENT_ID_PREFIX must be x509_san_dns or x509_hash')

def run(*args):
    subprocess.run(args, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)

with tempfile.TemporaryDirectory(prefix='verify-link-contract-') as tmp:
    p = Path(tmp)
    run('openssl', 'req', '-x509', '-newkey', 'ec', '-pkeyopt', 'ec_paramgen_curve:P-256', '-nodes', '-keyout', str(p/'ca.key'), '-out', str(p/'ca.pem'), '-days', '1', '-subj', '/CN=Verify Link untrusted contract fixture CA')
    run('openssl', 'req', '-newkey', 'ec', '-pkeyopt', 'ec_paramgen_curve:P-256', '-nodes', '-keyout', str(p/'rp.key'), '-out', str(p/'rp.csr'), '-subj', '/CN=verify.krnali.io')
    san = 'URI:https://verify.krnali.io' if CLIENT_ID_PREFIX == 'x509_hash' else 'DNS:verify.krnali.io'
    (p/'ext').write_text('subjectAltName='+san+'\nbasicConstraints=CA:FALSE\nkeyUsage=digitalSignature\n')
    run('openssl', 'x509', '-req', '-in', str(p/'rp.csr'), '-CA', str(p/'ca.pem'), '-CAkey', str(p/'ca.key'), '-CAcreateserial', '-out', str(p/'rp.pem'), '-days', '1', '-extfile', str(p/'ext'))
    run('openssl', 'pkcs12', '-export', '-inkey', str(p/'rp.key'), '-in', str(p/'rp.pem'), '-certfile', str(p/'ca.pem'), '-out', str(p/'rp.p12'), '-passout', 'pass:fixture-only', '-name', 'rp')
    (p/'rp.p12').chmod(0o644)
    # A signed but deliberately untrusted registration certificate for the HTTP contract.
    der=subprocess.check_output(['openssl','x509','-in',str(p/'rp.pem'),'-outform','DER'])
    b64=lambda b:base64.urlsafe_b64encode(b).rstrip(b'=').decode()
    header={'alg':'ES256','typ':'rc-wrp+jwt','x5c':[base64.b64encode(der).decode()]}
    payload={'sub':'contract-fixture','iat':int(time.time()),'exp':int(time.time())+3600}
    signing_input=b64(json.dumps(header).encode())+'.'+b64(json.dumps(payload).encode())
    signature=subprocess.check_output(['openssl','dgst','-sha256','-sign',str(p/'rp.key')],input=signing_input.encode())
    rlen=signature[3];r=int.from_bytes(signature[4:4+rlen]);offset=4+rlen;slen=signature[offset+1];s=int.from_bytes(signature[offset+2:offset+2+slen])
    (p/'registration.jwt').write_text(signing_input+'.'+b64(r.to_bytes(32,'big')+s.to_bytes(32,'big')))
    # Exercise the registry export wrapper with real disposable P12/JWT material.
    for kind, name in [('p12', 'rp.p12'), ('registration', 'registration.jwt')]:
        original = (p/name).read_bytes()
        encode = base64.b64encode if kind == 'p12' else base64.urlsafe_b64encode
        envelope = {'status':'success','code':200,'data':{'file_base64':encode(original).decode()}}
        (p/'export.json').write_text(json.dumps(envelope))
        output = p/('unpacked-'+name)
        run(sys.executable, str(ROOT/'scripts/unpack-eudi-export.py'), kind, str(p/'export.json'), '--out', str(output))
        expected = original if kind == 'p12' else original.rstrip(b'\n')+b'\n'
        assert output.read_bytes() == expected, 'registry export round trip changed material'
        output.replace(p/name)
        (p/name).chmod(0o644)  # Untrusted fixtures mounted into non-root containers only.
    (p/'logback.xml').write_text('<configuration><root level="OFF"/></configuration>\n')
    (p/'logback.xml').chmod(0o644)
    env = {
        'VERIFIER_PUBLICURL':'https://verify.krnali.io', 'VERIFIER_CLIENTIDPREFIX':CLIENT_ID_PREFIX,
        'VERIFIER_ORIGINALCLIENTID':b64(hashlib.sha256(der).digest()) if CLIENT_ID_PREFIX == 'x509_hash' else 'verify.krnali.io',
        'VERIFIER_ACCESS_CERTIFICATE_KEYSTORE':'file:///certs/rp.p12',
        'VERIFIER_ACCESS_CERTIFICATE_KEYSTORE_TYPE':'pkcs12',
        'VERIFIER_ACCESS_CERTIFICATE_KEYSTORE_PASSWORD':'fixture-only',
        'VERIFIER_ACCESS_CERTIFICATE_PASSWORD':'fixture-only',
        'VERIFIER_ACCESS_CERTIFICATE_ALIAS':'rp', 'VERIFIER_ACCESS_CERTIFICATE_SIGNING_ALGORITHM':'ES256',
        'VERIFIER_INTENDEDUSES':'', 'VERIFIER_MAXAGE':'PT5M', 'VERIFIER_PRESENTATIONS_CLEANUP_MAXAGE':'PT10M',
        # Only this loopback fixture allows HTTP callbacks. Deployment stays HTTPS-only.
        'VERIFIER_ALLOWEDREDIRECTURISCHEMES':'https,http',
        'LOGGING_CONFIG':'file:/config/logback.xml', 'LOGGING_LEVEL_ROOT':'OFF', 'LOGGING_LEVEL_ORG_SPRINGFRAMEWORK':'OFF',
        'BPL_JVM_THREAD_COUNT':'30', 'BPL_JVM_HEAD_ROOM':'10',
        'JAVA_TOOL_OPTIONS':'-XX:-HeapDumpOnOutOfMemoryError -XX:ErrorFile=/dev/null -XX:-UsePerfData',
    }
    if os.environ.get('REFERENCE_DEBUG')=='1':
        env.pop('LOGGING_CONFIG')
        env['LOGGING_LEVEL_ROOT']='INFO'
    args=['docker','run','-d','--read-only','--tmpfs','/tmp','--memory','1536m','--security-opt','no-new-privileges','--cap-drop','ALL','-p','127.0.0.1:18093:8080','-v',str(p/'rp.p12')+':/certs/rp.p12:ro','-v',str(p/'logback.xml')+':/config/logback.xml:ro']
    for k,v in env.items(): args += ['-e',k+'='+v]
    container=subprocess.check_output(args+[IMAGE],text=True).strip()
    try:
        for attempt in range(120):
            try:
                with urllib.request.urlopen('http://127.0.0.1:18093/actuator/health',timeout=1) as response:
                    if response.status==200:break
            except Exception:time.sleep(0.5)
        else:
            if os.environ.get('REFERENCE_DEBUG')=='1':
                subprocess.run(['docker','logs',container],check=False)
            raise RuntimeError('Reference fixture did not become healthy')
        subprocess.run(['docker','run','--rm','--network','host','-v',str(ROOT)+':/src','-v',str(p/'ca.pem')+':/issuer.pem:ro','-v',str(p/'registration.jwt')+':/registration.jwt:ro','-w','/src','-e','REFERENCE_VERIFIER_URL=http://127.0.0.1:18093','-e','REFERENCE_ISSUER_CHAIN_FILE=/issuer.pem','-e','REFERENCE_REGISTRATION_CERTIFICATE_FILE=/registration.jwt',GO,'go','test','-v','-run','TestReferenceContract','./internal/verifier'],check=True)
        print('PASS: reference startup, pinned HTTP init/poll contract, invalid mdoc refusal; synthetic trust only')
        app_env={'PUBLIC_BASE_URL':'http://localhost:18094','LISTEN_ADDR':'127.0.0.1:18094','VERIFIER_INTERNAL_URL':'http://127.0.0.1:18093','ISSUER_CHAIN_FILE':'/issuer.pem','REGISTRATION_CERTIFICATE_FILE':'/registration.jwt','BUSINESS_NAME':'Contract fixture','OPERATORS':'fixture@example.test:supervisor','OPERATOR_USER':'fixture@example.test','OPERATOR_PASSWORD':'fixture-only'}
        args=['docker','run','-d','--read-only','--network','host','--memory','256m','--security-opt','no-new-privileges','--cap-drop','ALL','-v',str(p/'ca.pem')+':/issuer.pem:ro','-v',str(p/'registration.jwt')+':/registration.jwt:ro']
        for k,v in app_env.items():args+=['-e',k+'='+v]
        app=subprocess.check_output(args+[os.environ.get('VERIFYLINK_IMAGE','message-verifier:local')],text=True).strip()
        try:
            def request(method,path,body=None,operator=False):
                headers={'Origin':'http://localhost:18094','Content-Type':'application/json'}
                if operator:headers['Authorization']='Basic '+base64.b64encode(b'fixture@example.test:fixture-only').decode()
                req=urllib.request.Request('http://127.0.0.1:18094'+path,method=method,data=None if body is None else json.dumps(body).encode(),headers=headers)
                try:
                    with urllib.request.urlopen(req,timeout=15) as r:return r.status,r.read()
                except urllib.error.HTTPError as e:return e.code,e.read()
            for attempt in range(50):
                try:
                    if request('GET','/healthz')[0]==200:break
                except OSError:time.sleep(0.1)
            else:raise RuntimeError('Go application fixture did not become healthy')
            code,data=request('POST','/api/sessions',{'channel':'copylink','recipient':'synthetic contract recipient','preset':'confirm-name'},True)
            assert code==201,'Go create failed';session=json.loads(data);path=urllib.parse.urlsplit(session['link']).path
            assert request('GET',path)[0]==200,'handoff failed'
            code,data=request('POST',path+'/start',{});assert code==200,'real verifier handoff failed: HTTP '+str(code);start=json.loads(data)
            auth=urllib.parse.urlsplit(start['authorization_request_uri']);assert auth.scheme=='haip-vp'
            expected_client_id = CLIENT_ID_PREFIX+':'+env['VERIFIER_ORIGINALCLIENTID']
            assert urllib.parse.parse_qs(auth.query)['client_id']==[expected_client_id], 'authorization client ID mismatch'
            uri=urllib.parse.parse_qs(auth.query)['request_uri'][0];wallet_path=urllib.parse.urlsplit(uri).path
            code,jar=request('GET',wallet_path);assert code==200 and len(jar.split(b'.'))==3,'wallet request object proxy failed'
            jar_payload=jar.split(b'.')[1]
            jar_claims=json.loads(base64.urlsafe_b64decode(jar_payload+b'='*(-len(jar_payload)%4)))
            assert jar_claims['client_id']==expected_client_id, 'signed request client ID mismatch'
            assert request('GET','/ui/presentations')[0]==404,'private UI exposed'
            assert request('GET','/utilities/validations/msoMdoc/deviceResponse')[0]==404,'private utility exposed'
            assert json.loads(request('GET','/s/'+start['poll_key'])[1])['status']=='verifying'
            assert request('POST',path+'/start',{})[0]==410,'link replay allowed'
            assert request('POST','/api/sessions/'+session['id']+'/cancel',{},True)[0]==200
            print('PASS: registry export round trip, '+CLIENT_ID_PREFIX+' client ID, built Go image configuration, authenticated create, real verifier start, signed request proxy, polling, replay and cancellation')
        finally:
            subprocess.run(['docker','rm','-f',app],stdout=subprocess.DEVNULL,check=False)
    finally:
        subprocess.run(['docker','rm','-f',container],stdout=subprocess.DEVNULL,check=False)
