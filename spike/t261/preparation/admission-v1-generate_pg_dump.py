"""One bounded lead-authorized supplementary dump; no SQL parser invocation."""
from pathlib import Path
import hashlib
import json
import os
import selectors
import subprocess
import time

ROOT=Path(__file__).resolve().parent
DOCKER="/opt/homebrew/bin/docker"
COLIMA="/opt/homebrew/bin/colima"
CONTEXT="colima-t574-receipts"
PROFILE="t574-receipts"
IMAGE="docker.io/library/postgres@sha256:a63b24165ea0eacced7585766d790ac08bbaefe10a1841f87783a4505d29a69c"
CONFIG="sha256:0336095afa072c685c93021dcfa4a5fee9cbce25ecd65121b58226c787afc0d0"
NAME="phebs-t261-pg18-fixture"
D=[DOCKER,"--context",CONTEXT]
RECEIPT=ROOT/"pg-generation-receipt.json"


def command(argv,timeout=30,check=True):
    started=time.monotonic()
    p=subprocess.run(argv,capture_output=True,timeout=timeout)
    if check and p.returncode:
        raise RuntimeError(f"command failed {argv[0:4]}: {p.returncode}: {p.stderr.decode(errors='replace')[:2048]}")
    return dict(argv=argv,exit_code=p.returncode,wall_seconds=round(time.monotonic()-started,6),stdout=p.stdout.decode(errors="replace"),stderr=p.stderr.decode(errors="replace"))


def save(value):
    RECEIPT.write_text(json.dumps(value,indent=2)+"\n")


def main():
    assert not RECEIPT.exists(),"One execution only; retained receipt already exists."
    raw=(ROOT/"fixtures/requested-pg-input.sql").read_bytes()
    assert hashlib.sha256(raw).hexdigest()=="dd7153ba4733be4d24b26993f8762d98d05d8754691194c41266f824c2253273"
    script=ROOT/"fixtures/generate-pg18-container.sh"
    config_file=Path("/Users/ben/.colima/t574-receipts/colima.yaml")
    receipt=dict(format="t261-pg-generation-receipt-v1",date="2026-10-18",stage="admitting exact image; no container initdb yet",input_sha256=hashlib.sha256(raw).hexdigest(),input_bytes=len(raw),script_sha256=hashlib.sha256(script.read_bytes()).hexdigest(),image_ref=IMAGE,platform="linux/arm64/v8",docker_context=CONTEXT,vm_profile=PROFILE,vm_configuration_sha256=hashlib.sha256(config_file.read_bytes()).hexdigest(),vm_reused_unchanged=True,vm_start_argv=[COLIMA,"start","--profile",PROFILE],original_docker_context="colima",container_execution_cap_seconds=60,stdout_acceptance_cap_bytes=65536,stderr_cap_bytes=65536,parser_runs=0,recognizer_runs=0,steps=[],teardown={},status="pending")
    cid=None
    proc=None
    out=bytearray()
    err=bytearray()
    failure=None
    save(receipt)
    try:
        receipt["steps"].append(command([COLIMA,"status","--profile",PROFILE]))
        server=command(D+["info","--format","{{json .}}"])
        info=json.loads(server["stdout"])
        receipt["docker_server"]={k:info.get(k) for k in ["Architecture","OSType","NCPU","MemTotal","ServerVersion","OperatingSystem","Name"]}
        assert info["OSType"]=="linux" and info["Architecture"] in ["aarch64","arm64"]
        existing=command(D+["container","inspect",NAME],check=False)
        assert existing["exit_code"]!=0,"Refuse existing container name; no unrelated cleanup."
        receipt["steps"].append(command(D+["pull","--platform","linux/arm64/v8",IMAGE],timeout=180))
        image=json.loads(command(D+["image","inspect",IMAGE])["stdout"])[0]
        assert image["Id"]==CONFIG and image["Architecture"]=="arm64" and image["Os"]=="linux"
        receipt["admitted_image"]={k:image.get(k) for k in ["Id","RepoDigests","Architecture","Os","Size","Created"]}
        create=D+["create","--name",NAME,"--platform","linux/arm64/v8","--network","none","--user","999:999","--read-only","--cap-drop=ALL","--security-opt=no-new-privileges","--memory=256m","--memory-swap=256m","--cpus=1","--pids-limit=64","--shm-size=32m","--tmpfs","/work:rw,noexec,nosuid,size=134217728,uid=999,gid=999,mode=0700","--tmpfs","/var/lib/postgresql:rw,noexec,nosuid,size=16777216,uid=999,gid=999,mode=0700","--mount",f"type=bind,src={ROOT/'fixtures/requested-pg-input.sql'},dst=/fixture/input.sql,readonly","--mount",f"type=bind,src={script},dst=/fixture/generate.sh,readonly","--entrypoint","/bin/bash",IMAGE,"/fixture/generate.sh"]
        receipt["literal_create_argv"]=create
        receipt["literal_server_argv"]=["/usr/lib/postgresql/18/bin/postgres","-D","/work/data","-c","listen_addresses=","-c","unix_socket_directories=/work/socket","-c","unix_socket_permissions=0700","-c","port=52611","-c","max_connections=4","-c","shared_buffers=16MB","-c","max_wal_size=64MB","-c","min_wal_size=32MB","-c","logging_collector=off"]
        receipt["literal_load_argv"]=["/usr/lib/postgresql/18/bin/psql","-X","--no-password","--set=ON_ERROR_STOP=1","--host=/work/socket","--port=52611","--username=t261","--dbname=postgres","--file=/fixture/input.sql"]
        receipt["literal_dump_argv"]=["/usr/lib/postgresql/18/bin/pg_dump","--schema-only","--no-owner","--no-privileges","--no-comments","--no-security-labels","--no-tablespaces","--restrict-key=phebsT261PinnedPG186Fixture","--no-password","--host=/work/socket","--port=52611","--username=t261","--dbname=postgres"]
        receipt["stage"]="literal image/source/script/argv frozen before container creation and initdb"
        save(receipt)
        created=command(create)
        cid=created["stdout"].strip()
        assert len(cid)==64 and all(c in "0123456789abcdef" for c in cid)
        receipt["container_id"]=cid
        receipt["steps"].append(created)
        start=D+["start","--attach",cid]
        receipt["literal_start_argv"]=start
        receipt["stage"]="one bounded container execution"
        save(receipt)
        started=time.monotonic()
        proc=subprocess.Popen(start,stdout=subprocess.PIPE,stderr=subprocess.PIPE,start_new_session=True)
        selector=selectors.DefaultSelector()
        for stream,destination in [(proc.stdout,out),(proc.stderr,err)]:
            os.set_blocking(stream.fileno(),False)
            selector.register(stream,selectors.EVENT_READ,destination)
        try:
            while selector.get_map():
                if time.monotonic()-started>=60:
                    raise TimeoutError("Whole-container execution exceeded60seconds")
                for key,_ in selector.select(timeout=0.05):
                    chunk=os.read(key.fileobj.fileno(),4096)
                    if not chunk:
                        selector.unregister(key.fileobj)
                        continue
                    key.data.extend(chunk)
                    if len(key.data)>65536:
                        raise RuntimeError("Container output exceeded65536-byte cap")
            proc.wait(timeout=max(0.001,60-(time.monotonic()-started)))
        finally:
            selector.close()
        receipt["execution_wall_seconds"]=round(time.monotonic()-started,6)
        receipt["docker_cli_exit_code"]=proc.returncode
        assert proc.returncode==0,err.decode(errors="replace")
        receipt["container_wait"]=command(D+["wait",cid],timeout=10)
        assert receipt["container_wait"]["stdout"].strip()=="0"
        inspection=json.loads(command(D+["container","inspect",cid])["stdout"])[0]
        receipt["container_final_state"]=inspection["State"]
        receipt["container_host_config"]={k:inspection["HostConfig"].get(k) for k in ["NetworkMode","ReadonlyRootfs","Memory","MemorySwap","NanoCpus","PidsLimit","ShmSize","Tmpfs","PortBindings"]}
        assert inspection["State"]["Pid"]==0 and not inspection["State"]["Running"]
        assert inspection["HostConfig"]["NetworkMode"]=="none" and not inspection["HostConfig"]["PortBindings"]
        transcript=err.decode()
        assert "t261_double_dump_identical=true" in transcript and "t261_server_joined=true" in transcript and "t261_server_exit=0" in transcript
        digest=hashlib.sha256(out).hexdigest()
        assert transcript.count(digest)==2
        assert 0<len(out)<=65536
        (ROOT/"fixtures/requested-pg18.6-dump.sql").write_bytes(out)
        receipt.update(output_path="fixtures/requested-pg18.6-dump.sql",output_bytes=len(out),output_sha256=digest,double_dump_identical=True,status="passed")
    except Exception as exc:
        failure=exc
        receipt.update(status="failed",failure_type=type(exc).__name__,failure=str(exc)[:8192])
    finally:
        receipt["stdout_bytes"]=len(out)
        receipt["stderr_transcript"]=err.decode(errors="replace")
        if cid is not None:
            for key,argv in [("stop",D+["stop","--time","10",cid]),("wait",D+["wait",cid]),("remove",D+["rm","--force","--volumes",cid])]:
                try:receipt["teardown"][key]=command(argv,timeout=15,check=False)
                except Exception as exc:receipt["teardown"][key]=dict(error=str(exc))
            receipt["teardown"]["container_absent"]=command(D+["container","inspect",cid],timeout=10,check=False)["exit_code"]!=0
        if proc is not None:
            if proc.poll() is None:
                proc.terminate()
                try:proc.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    proc.kill();proc.wait(timeout=5)
            for stream in [proc.stdout,proc.stderr]:stream.close()
            receipt["teardown"]["docker_cli_joined"]=True
            receipt["teardown"]["docker_cli_final_exit_code"]=proc.returncode
        receipt["teardown"]["context_restore"]=command([DOCKER,"context","use","colima"],check=False)
        receipt["teardown"]["vm_stop"]=command([COLIMA,"stop","--profile",PROFILE],timeout=45,check=False)
        receipt["teardown"]["vm_stopped"]=command([COLIMA,"status","--profile",PROFILE],check=False)["exit_code"]!=0
        receipt["teardown"]["docker_context_restored"]=command([DOCKER,"context","show"])["stdout"].strip()=="colima"
        receipt["vm_configuration_unchanged"]=hashlib.sha256(config_file.read_bytes()).hexdigest()==receipt["vm_configuration_sha256"]
        receipt["stage"]="finished; source-only preparation resumes; no parser executed"
        save(receipt)
    assert receipt["teardown"].get("container_absent",cid is None)
    assert receipt["teardown"]["vm_stopped"] and receipt["teardown"]["docker_context_restored"]
    assert receipt["vm_configuration_unchanged"]
    if failure:raise failure
    print(json.dumps({k:receipt[k] for k in ["status","output_bytes","output_sha256","execution_wall_seconds","teardown"]},indent=2))


if __name__=="__main__":main()
