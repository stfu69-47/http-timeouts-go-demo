"""Validate all fresh records and correlate HTTP IDs with both TCP captures.
Usage: python tools/analyze_run.py RUN_DIRECTORY
"""
import csv, datetime, hashlib, io, json, math, re, subprocess, sys
from pathlib import Path
R=Path(sys.argv[1]).resolve(); A=R/'analysis'; A.mkdir(exist_ok=True)
network_runs=list((R/'network').glob('run-*'))
assert len(network_runs)==1, 'Choose one fresh run directory; do not combine network repetitions'
N=network_runs[0]
def dump(path,obj): path.write_text(json.dumps(obj,ensure_ascii=False,indent=2)+'\n')
def quant(vals):
    v=sorted(vals); n=len(v)
    return {'n':n,**{k:(v[math.ceil(p*n)-1] if n else None) for k,p in [('p50_ms',.5),('p95_ms',.95),('p99_ms',.99)]},'max_ms':max(v) if n else None}
def rows(p): return [json.loads(s) for s in p.read_text().splitlines()]
def failed(r): return any(r.get(k) for k in ['do_error','body_error','close_error'])
checks=[]; summaries={}
for group,paths in [('suite',sorted((R/'suite').glob('*.jsonl'))),('observations',sorted((R/'observations').glob('*.jsonl'))),('network',sorted(N.glob('*.jsonl')))]:
    for p in paths:
        rr=rows(p); name=p.stem
        ss=next(s for s in json.loads((p.parent/'summary.json').read_text()) if s['scenario']==name) if group=='suite' else json.loads(Path(str(p)+'.summary.json').read_text())
        assert len(rr)==ss['requests'] and sorted(r['id'] for r in rr)==list(range(1,len(rr)+1)),p
        assert sum(failed(r) for r in rr)==ss['io_errors'],p
        for key,pct in [('p50_ms',.5),('p95_ms',.95),('p99_ms',.99)]:
            assert abs(quant([r['duration_ms'] for r in rr])[key]-ss['latency_all'][key])<1e-8,p
        for r in rr:
            assert math.isfinite(r['duration_ms']) and r['duration_ms']>=0
            if not failed(r):
                assert r['status']==200 and r['protocol']=='HTTP/1.1' and r['body_bytes']==64
            assert any(e['name']=='RequestDone' for e in r['events'])
            assert all(e['at_ms']<=r['trace_snapshot_ms'] for e in r['events'])
        checks.append({'group':group,'scenario':name,'records':len(rr),'io_errors':ss['io_errors'],'unique_contiguous_ids':True,'percentiles_match':True,'sha256':hashlib.sha256(p.read_bytes()).hexdigest()})
        summaries[group+'/'+name]=ss
assert [sum(c['records'] for c in checks if c['group']==g) for g in ['suite','observations','network']]==[1690,200,3000]
assert sum(c['io_errors'] for c in checks)==40
for name in ['rare_headers','rare_body']:
    assert [r['id'] for r in rows(R/'suite'/f'{name}.jsonl') if r['fixture_slow']]==list(range(50,501,50))
assert all(r.get('do_error') and r['timed_out'] for r in rows(R/'suite'/'header_timeout.jsonl'))
assert all(r.get('body_error') and r['status']==200 and r['timed_out'] for r in rows(R/'suite'/'body_timeout.jsonl'))
status=dict(s.split('=',1) for s in (N/'status.txt').read_text().splitlines())
assert all(status[k]==v for k,v in [('measurements_complete','true'),('exit_status','0'),('cleanup_exit_status','0')])
for p in [R/'checks'/'suite.stderr',R/'checks'/'observe.stderr',*(R/'observations').glob('*.stderr.txt'),*N.glob('*.probe.stderr')]: assert not p.read_text(),p
sockets={}
for p in (R/'observations').glob('*.ss.txt'):
    t=p.read_text(); assert 'ESTAB' in t and 'rtt:' in t and 'rto:' in t
    assert not re.search('not found|Permission denied|Error:',t)
    sockets[p.name]={'samples':len(re.findall(r'^2026-',t,re.M)),'established_rows':len(re.findall(r'^ESTAB',t,re.M))}
fields=['frame.number','frame.time_epoch','ip.src','tcp.srcport','ip.dst','tcp.dstport','tcp.stream','tcp.seq_raw','tcp.ack_raw','tcp.len','tcp.flags','tcp.analysis.retransmission','tcp.analysis.fast_retransmission','tcp.analysis.spurious_retransmission','tcp.payload']
commands=[]; capture=[]; analysis={}; evidence=[]
def tup(p):
    if p['tcp.dstport']=='8080': return (p['ip.src'],p['tcp.srcport'],p['ip.dst'],'8080')
    return (p['ip.dst'],p['tcp.dstport'],p['ip.src'],'8080')
def ts(p):return float(p['frame.time_epoch'])
def isret(p):return any(p[k] for k in fields[11:14])
for case in ['baseline','delay20ms','delay20ms-loss1pct']:
    packets={}; caps=[]
    for side in ['client','server']:
        f=N/f'{case}.{side}.pcap'; out=A/f'{case}.{side}.packets.tsv'
        cmd=['tshark','-n','-r',str(f),'-T','fields','-E','header=y','-E','separator=/t','-E','quote=d','-E','occurrence=f']
        for field in fields: cmd+=['-e',field]
        commands.append(cmd)
        with out.open('w') as stdout,(A/f'{case}.{side}.parse.stderr').open('w') as stderr:
            subprocess.run(cmd,stdout=stdout,stderr=stderr,check=True)
        pp=list(csv.DictReader(out.open(),delimiter='\t')); assert pp
        assert not (A/f'{case}.{side}.parse.stderr').read_text()
        for p in pp:
            payload=bytes.fromhex(p['tcp.payload'].replace(':','')).decode('latin1') if p['tcp.payload'] else ''
            match=re.search(r'X-Lab-Request-Id: (\d+)',payload,re.I)
            p['request_id']=int(match.group(1)) if match else None
            p['response']=payload.startswith('HTTP/1.1 200')
        packets[side]=pp
        ct=(N/f'{case}.{side}.capture.stderr').read_text()
        cap={'case':case,'side':side,'pcap_frames':len(pp),'captured':int(re.search(r'(\d+) packets captured',ct)[1]),'received_by_filter':int(re.search(r'(\d+) packets received by filter',ct)[1]),'dropped_by_kernel':int(re.search(r'(\d+) packets dropped by kernel',ct)[1]),'retransmission_frames':sum(isret(p) for p in pp),'http_ids':len({p['request_id'] for p in pp if p['request_id']}),'response_frames':sum(p['response'] for p in pp),'sha256':hashlib.sha256(f.read_bytes()).hexdigest()}
        assert cap['pcap_frames']==cap['captured'] and cap['http_ids']==1000 and cap['response_frames']==1000
        capture.append(cap);caps.append(cap)
    rr=rows(N/f'{case}.jsonl'); byid={r['id']:r for r in rr}; linked=[]
    for rid,r in byid.items():
        c=[p for p in packets['client'] if p['request_id']==rid]
        s=[p for p in packets['server'] if p['request_id']==rid]
        assert c and s
        assert len(c)==len(s)
        assert all((p['tcp.seq_raw'],p['tcp.payload'],tup(p))==(c[0]['tcp.seq_raw'],c[0]['tcp.payload'],tup(c[0])) for p in c+s)
        conn=tup(c[0]); got=next(e for e in r['events'] if e['name']=='GotConn')
        assert got['local_address']==':'.join(conn[:2]) and got['remote_address']==':'.join(conn[2:])
        ack=(int(c[0]['tcp.seq_raw'])+int(c[0]['tcp.len'])) % (2**32)
        cr=[p for p in packets['client'] if tup(p)==conn and p['response'] and int(p['tcp.ack_raw'])==ack and ts(p)>=ts(c[0])]
        sr=[p for p in packets['server'] if tup(p)==conn and p['response'] and int(p['tcp.ack_raw'])==ack and ts(p)>=ts(s[0])]
        assert len(cr)==len(sr)==1,(case,rid,len(cr),len(sr))
        assert (cr[0]['tcp.seq_raw'],cr[0]['tcp.ack_raw'],cr[0]['tcp.payload'])==(sr[0]['tcp.seq_raw'],sr[0]['tcp.ack_raw'],sr[0]['tcp.payload'])
        item={'case':case,'id':rid,'duration_ms':r['duration_ms'],'tuple':list(conn),'client_stream':c[0]['tcp.stream'],'server_stream':s[0]['tcp.stream'],'request_frames_client':[p['frame.number'] for p in c],'request_frames_server':[p['frame.number'] for p in s],'response_frame_client':cr[0]['frame.number'],'response_frame_server':sr[0]['frame.number'],'request_seq':int(c[0]['tcp.seq_raw']),'request_len':int(c[0]['tcp.len']),'response_seq':int(cr[0]['tcp.seq_raw']),'response_ack':int(cr[0]['tcp.ack_raw']),'response_len':int(cr[0]['tcp.len']),'request_retransmissions':sum(isret(p) for p in c),'request_to_response_capture_ms':1000*(ts(cr[0])-ts(c[0])),'server_request_to_response_capture_ms':1000*(ts(sr[0])-ts(s[0])),'retry_after_ms':1000*(ts(c[-1])-ts(c[0])) if len(c)>1 else None,'first_request_epoch':ts(c[0]),'response_epoch':ts(cr[0])}
        # Find the cumulative ACK of the response; a subsequent HTTP request may carry it.
        response_end=(item['response_seq']+item['response_len'])%(2**32)
        ackframes=[p for p in packets['client'] if tup(p)==conn and p['tcp.dstport']=='8080' and int(p['tcp.ack_raw'])==response_end and ts(p)>=ts(cr[0])]
        item['response_ack_frame_client']=ackframes[0]['frame.number'] if ackframes else None
        linked.append(item)
    dump(A/f'{case}.request-packet-links.json',linked)
    retried=[x for x in linked if x['request_retransmissions']]
    clean=[x for x in linked if not x['request_retransmissions']]
    counters={side:{r['counter']:int(r['delta']) for r in csv.DictReader((N/f'{case}.{side}.nstat.delta.csv').open())} for side in ['client','server']}
    for side in counters:
        def read_counters(point):
            return {v[0]:int(v[1]) for line in (N/f'{case}.{side}.nstat.{point}.txt').read_text().splitlines() if len(v:=line.split())>=2 and not v[0].startswith('#')}
        before,after=read_counters('before'),read_counters('after')
        assert all(after[k]-before[k]==v for k,v in counters[side].items())
    analysis[case]={'summary':summaries['network/'+case],'retransmitted_requests':len(retried),'retry_latency':quant([x['duration_ms'] for x in retried]),'no_retry_latency':quant([x['duration_ms'] for x in clean]),'captures':caps,'counters':{side:{k:counters[side].get(k) for k in ['TcpRetransSegs','TcpExtTCPTimeouts','TcpExtTCPLossProbes','TcpExtTCPLossProbeRecovery','TcpExtTCPSynRetrans']} for side in counters},'slowest':sorted(linked,key=lambda x:x['duration_ms'],reverse=True)[:12]}
    evidence.extend(retried)
dump(A/'validation.json',{'passed':True,'records':4890,'io_errors':40,'checks':checks,'sockets':sockets,'network_status':status,'captures':capture})
dump(A/'summaries.json',summaries); dump(A/'network-analysis.json',analysis);dump(A/'retransmission-evidence.json',evidence);dump(A/'tshark-commands.json',commands)
print(json.dumps({'validated':4890,'expected_errors':40,'network':{k:{'latency':v['summary']['latency_all'],'retried_http':v['retransmitted_requests'],'counters':v['counters']} for k,v in analysis.items()}},indent=2))
