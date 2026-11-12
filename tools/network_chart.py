import hashlib,json,sys
from pathlib import Path
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt
R=Path(sys.argv[1]).resolve();A=R/'analysis';O=R/'assets'
data=json.loads((A/'network-analysis.json').read_text())
plt.rcParams.update({'font.family':'DejaVu Sans','font.size':11,'axes.spines.top':False,'axes.spines.right':False})
fig,(ax,bx)=plt.subplots(2,1,figsize=(11.5,8.5),gridspec_kw={'height_ratios':[1,1.3]})
colors=['#2262b8','#00897d','#c66d21'];cases=list(data)
for j,(q,label) in enumerate([('p50_ms','p50'),('p95_ms','p95'),('p99_ms','p99')]):
    ys=[i+(j-1)*.23 for i in range(3)];vals=[data[c]['summary']['latency_all'][q] for c in cases]
    ax.barh(ys,vals,height=.21,color=colors[j],label=label)
    for y,v in zip(ys,vals):ax.text(v+.5,y,f'{v:.2f}',va='center',fontsize=9)
ax.set_yticks(range(3),['Без воздействия','Задержка 20 мс','20 мс + потери 1%']);ax.invert_yaxis();ax.set_xlim(0,62)
ax.set_xlabel('Полное время запроса, мс');ax.legend(ncol=3,frameon=False,loc='upper right');ax.set_title('Три сетевых сценария по 1000 запросов',loc='left',weight='bold')
links=json.loads((A/'delay20ms-loss1pct.request-packet-links.json').read_text())
for retried,color,label in [(False,'#2262b8','Без наблюдаемого повтора запроса'),(True,'#c66d21','Повтор TCP с тем же номером последовательности')]:
    pp=[p for p in links if bool(p['request_retransmissions'])==retried]
    bx.scatter([p['id'] for p in pp],[p['duration_ms'] for p in pp],s=35 if retried else 8,color=color,label=label,zorder=3 if retried else 2)
    if retried:
        for p in pp:
            if p['id']==64: continue
            label='61, 64' if p['id']==61 else str(p['id'])
            bx.annotate(label,(p['id'],p['duration_ms']),xytext=(0,8),textcoords='offset points',ha='center')
bx.set_xlabel('Номер HTTP-запроса');bx.set_ylabel('Полное время, мс');bx.set_ylim(0,290);bx.legend(loc='center',frameon=False,fontsize=10)
bx.set_title('Четыре медленных запроса связаны с повторной передачей',loc='left',weight='bold');bx.grid(alpha=.2)
fig.tight_layout(rect=(0,.06,1,1));fig.text(.04,.015,'Linux/arm64 · Go 1.27.4 · OrbStack · HTTP/1.1 · 8 работников · начальные соединения включены\nПотери 1% — заданное правило netem; это не доля ошибочных HTTP-запросов. Все 3000 ответов прочитаны.',fontsize=9)
for ext in ['png','svg']:fig.savefig(O/f'04_network_latency.{ext}',dpi=180,bbox_inches='tight')
(O/'network-chart-provenance.json').write_text(json.dumps({'sources':{str(p.relative_to(R)):hashlib.sha256(p.read_bytes()).hexdigest() for p in [A/'network-analysis.json',A/'delay20ms-loss1pct.request-packet-links.json']},'method':'nearest rank of all attempts; scatter each duration_ms; highlight TCP repeated request payload and sequence mapped by four tuple + X-Lab-Request-ID'},indent=2)+'\n')
