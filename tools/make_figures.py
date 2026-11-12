from pathlib import Path
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt
from matplotlib.patches import FancyBboxPatch

import argparse
p=argparse.ArgumentParser(); p.add_argument('--out',type=Path,required=True)
OUT=p.parse_args().out
OUT.mkdir(exist_ok=True)
plt.rcParams.update({'font.family':'DejaVu Sans','font.size':12,'text.color':'#14263d','savefig.facecolor':'white'})
def box(ax,x,y,w,h,label,face,color='white',size=13):
    ax.add_patch(FancyBboxPatch((x,y),w,h,boxstyle='round,pad=0.02,rounding_size=0.06',facecolor=face,edgecolor='none'))
    ax.text(x+w/2,y+h/2,label,ha='center',va='center',color=color,fontsize=size)
fig,ax=plt.subplots(figsize=(13,5.4))
ax.set_xlim(-.1,12.4);ax.set_ylim(-.1,5.2);ax.axis('off')
ax.text(0,4.95,'Где HTTP-запрос проводит время',fontsize=23,weight='bold')
ax.text(0,4.48,'HTTP/1.1 · один GET · без перенаправления',fontsize=13,color='#526578')
box(ax,0,3.62,12.1,.5,'Полное время: от запуска запроса до чтения тела и закрытия Body','#eef4fa','#14263d',13)
box(ax,0,2.28,4.95,.9,'Получение соединения','#2262b8',size=16)
box(ax,5.08,2.28,1.7,.9,'Запись\nзапроса','#00897d',size=13)
box(ax,6.91,2.28,2.65,.9,'Ожидание\nпервого байта','#be6b15',size=14)
box(ax,9.69,2.28,2.41,.9,'Заголовки\nи тело ответа','#bc4358',size=14)
for x,label in [(0,'Пул'),(1.27,'DNS'),(2.54,'TCP'),(3.81,'TLS')]:
    box(ax,x,1.37,1.14,.56,label,'#eef4fa','#14263d')
ax.text(2.47,.98,'Вложенные этапы, а не дополнительные слагаемые',ha='center',fontsize=10.5,color='#526578')
for x,label,ha in [(0,'GetConn','left'),(4.95,'GotConn','right'),(6.78,'WroteRequest','right'),(9.56,'GotFirstResponseByte','right'),(12.1,'EOF / Close','right')]:
    ax.plot([x,x],[3.2,3.38],color='#526578',lw=.8)
    ax.text(x,3.42,label,fontsize=9.5,ha=ha,color='#526578')
ax.text(6.91,1.49,'Первый байт ≠ полные заголовки\nи ≠ завершённый ответ',fontsize=12,va='center')
ax.text(0,.38,'При повторном использовании соединения DNS, TCP и TLS могут не выполняться.',fontsize=12)
ax.text(0,.02,'Схема условная. Ширина блоков не отражает измерения; порядок сложнее при 1xx и повторных попытках.',fontsize=10,color='#526578')
for ext in ['png','svg']:
    fig.savefig(OUT/f'01_request_timeline.{ext}',dpi=180,bbox_inches='tight',pad_inches=.24)
plt.close(fig)
print('Created timeline diagram')
