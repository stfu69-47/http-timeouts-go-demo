from pathlib import Path
import argparse, hashlib, json, math
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt
import numpy as np
ROOT=Path(__file__).resolve().parents[1]
p=argparse.ArgumentParser()
p.add_argument('--data', type=Path, default=ROOT/'http-timeout-lab'/'results')
p.add_argument('--out', type=Path, default=ROOT/'assets')
a=p.parse_args(); DATA=a.data.resolve(); OUT=a.out.resolve()
metadata=json.loads((DATA/'metadata.json').read_text())
OUT.mkdir(parents=True,exist_ok=True)
plt.rcParams.update({'font.family':'DejaVu Sans','font.size':12,'text.color':'#14263d','axes.labelcolor':'#526578','axes.spines.top':False,'axes.spines.right':False})
summary={s['scenario']:s for s in json.loads((DATA/'summary.json').read_text())}
def save(fig,name):
    for ext in ['png','svg']:fig.savefig(OUT/f'{name}.{ext}',dpi=180,bbox_inches='tight',pad_inches=.25)
    plt.close(fig)
cases=['baseline','rare_headers','rare_body']
labels=['Обычный ответ','Задержка заголовков у 2%','Задержка тела у 2%']
fig,ax=plt.subplots(figsize=(11.8,5.2));y=np.arange(3)
for offset,key,color,title in [(-.16,'p50_ms','#2262b8','p50'),(.16,'p99_ms','#c66d21','p99')]:
    vals=[summary[c]['latency_completed'][key] for c in cases]
    ax.barh(y+offset,vals,height=.27,color=color,label=title,zorder=3)
    for yi,v in zip(y+offset,vals):ax.text(v+4,yi,f'{v:.1f}',va='center',fontsize=12,color=color)
ax.set_yticks(y,labels);ax.invert_yaxis();ax.set_xlim(0,max(summary[c]['latency_completed']['p99_ms'] for c in cases)*1.21)
ax.set_xlabel('Полное время запроса, мс');ax.grid(axis='x',color='#e2e9f1',zorder=0)
ax.set_title('Редкие задержки меняют p99',loc='left',fontsize=22,pad=24)
ax.legend(loc='upper right',frameon=False,ncol=2)
counts=' / '.join(str(summary[c]['latency_completed']['n']) for c in cases)
fig.text(.01,-.02,f"{metadata.get('go_version', 'см. metadata.json')} · HTTP/1.1 · 8 работников · n = {counts}\nЗавершённые ответы без I/O-ошибок; базовая задержка 5 мс, дополнительная 300 мс.",fontsize=10,color='#526578')
save(fig,'02_latency_percentiles')
fig,ax=plt.subplots(figsize=(12,5.4));fig.subplots_adjust(bottom=.28);descs=[]
colors=['#2262b8','#00897d','#be6b15','#bc4358']
leg=['Старт → GotConn','Запись запроса','Ожидание первого байта','Первый байт → завершение']
for y,name in enumerate(['pool_1','pool_8']):
    records=[json.loads(l) for l in (DATA/f'{name}.jsonl').read_text().splitlines() if l]
    records.sort(key=lambda r:r['duration_ms']);r=records[math.ceil(len(records)*.5)-1]
    ev={e['name']:e['at_ms'] for e in r['events']}
    for event in ['GotConn','WroteRequest','GotFirstResponseByte']:
        assert sum(e['name']==event for e in r['events'])==1, 'Timeline requires one ordered exchange'
    points=[0,ev['GotConn'],ev['WroteRequest'],ev['GotFirstResponseByte'],r['duration_ms']]
    assert points==sorted(points), 'Timeline points must be ordered'
    for i,(left,right) in enumerate(zip(points,points[1:])):
        ax.barh(y,right-left,left=left,height=.43,color=colors[i],label=leg[i] if y==0 else None,zorder=3)
        if right-left>14:ax.text((left+right)/2,y,f'{right-left:.1f} мс',va='center',ha='center',color='white',fontsize=12)
    ax.text(points[-1]+3,y,f'{points[-1]:.1f} мс',va='center',weight='bold')
    descs.append({'scenario':name,'request_id':r['id'],'duration_ms':r['duration_ms'],'points_ms':points})
ax.set_yticks([0,1],['1 соединение','8 соединений']);ax.invert_yaxis();ax.set_xlim(0,max(d['duration_ms'] for d in descs)*1.22)
ax.set_xlabel('Время от старта запроса, мс');ax.grid(axis='x',color='#e2e9f1',zorder=0)
ax.set_title('Один обработчик, два лимита соединений',loc='left',fontsize=21,pad=24)
ax.legend(loc='upper left',bbox_to_anchor=(0,-.22),ncol=2,frameon=False,fontsize=10)
fig.text(.125,.01,'По одному запросу около медианы каждой серии · 100 запросов, 8 работников · обработчик ждёт 25 мс\nРеальные отдельные запросы, а не сумма перцентилей разных этапов.',fontsize=10,color='#526578')
save(fig,'03_pool_timeline')
(OUT/'chart-provenance.json').write_text(json.dumps({'source':str(DATA),'method':'nearest rank; pool request at ceil(n*0.5)-1 after duration sort','sha256':{f.name:hashlib.sha256(f.read_bytes()).hexdigest() for f in sorted(DATA.glob('*.json*'))},'latency_scenarios':cases,'pool_requests':descs},ensure_ascii=False,indent=2)+'\n')
print('Created measured charts')
