"""Measure separate fixture CPU and record real ss socket snapshots."""
import argparse, datetime, json, os, platform, signal, socket, subprocess, time
from pathlib import Path
p=argparse.ArgumentParser()
p.add_argument('--binary',required=True);p.add_argument('--out',required=True)
a=p.parse_args();binary=Path(a.binary).resolve();out=Path(a.out).resolve();out.mkdir(parents=True,exist_ok=True)
hz=os.sysconf('SC_CLK_TCK')
def ticks(pid):
    s=Path(f'/proc/{pid}/stat').read_text();f=s[s.rfind(')')+2:].split()
    return int(f[11])+int(f[12])
with socket.socket() as s:
    s.bind(('127.0.0.1',0));port=s.getsockname()[1]
address=f'127.0.0.1:{port}';rows=[]
log=(out/'server.log').open('w')
server=subprocess.Popen([str(binary),'serve','-listen',address,'-header-delay','25ms'],stdout=log,stderr=subprocess.STDOUT)
try:
    deadline=time.monotonic()+10
    while True:
        if server.poll() is not None:raise RuntimeError('Fixture exited')
        try:
            with socket.create_connection(('127.0.0.1',port),timeout=.2):break
        except OSError:
            if time.monotonic()>deadline:raise TimeoutError('Startup timeout')
            time.sleep(.05)
    for count in [1,8]:
        case=f'pool_{count}'
        cmd=[str(binary),'probe','-url',f'http://{address}/','-n','100','-c','8','-max-conns',str(count),'-timeout','5s','-out',str(out/f'{case}.jsonl')]
        before=ticks(server.pid);start=time.monotonic()
        with (out/f'{case}.stdout.txt').open('w') as stdout,(out/f'{case}.stderr.txt').open('w') as stderr,(out/f'{case}.ss.txt').open('w') as snap:
            probe=subprocess.Popen(cmd,stdout=stdout,stderr=stderr)
            try:
                while probe.poll() is None:
                    snap.write(datetime.datetime.now(datetime.timezone.utc).isoformat()+'\n')
                    r=subprocess.run(['ss','-tin',f'( sport = :{port} or dport = :{port} )'],capture_output=True,text=True,timeout=3)
                    snap.write(r.stdout+'\n'+r.stderr)
                    time.sleep(.1)
                    if time.monotonic()-start>30:raise TimeoutError('Probe budget')
                if probe.returncode:raise RuntimeError(f'probe exit {probe.returncode}')
            finally:
                if probe.poll() is None:probe.terminate();probe.wait(timeout=5)
        elapsed=time.monotonic()-start;cpu=(ticks(server.pid)-before)/hz
        rows.append({'case':case,'requests':100,'concurrency':8,'max_conns_per_host':count,'handler_delay_ms':25,'elapsed_seconds':elapsed,'server_cpu_seconds':cpu,'server_cpu_percent_one_logical_cpu':100*cpu/elapsed,'command':cmd})
finally:
    server.send_signal(signal.SIGTERM)
    try:server.wait(timeout=5)
    except subprocess.TimeoutExpired:server.kill();server.wait()
    log.close()
report={'recorded_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'system':platform.platform(),'clock_ticks_per_second':hz,'scope':'separate server process CPU; not host CPU','sampling':'ss every about 100ms; wall window includes final polling interval','runs':rows}
(out/'cpu-observation.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(report,ensure_ascii=False,indent=2))
