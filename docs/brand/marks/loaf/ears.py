import re, math, html
exec(open('gen5.py').read().split("V={}")[0])
def rrect(x0,y0,x1,y1,rt,rb):
    d=f"M{x0+rt:.2f} {y0:.2f}L{x1-rt:.2f} {y0:.2f}"+(f"A{rt:.2f} {rt:.2f} 0 0 1 {x1:.2f} {y0+rt:.2f}" if rt else "")
    d+=f"L{x1:.2f} {y1-rb:.2f}"+(f"A{rb:.2f} {rb:.2f} 0 0 1 {x1-rb:.2f} {y1:.2f}" if rb else f"L{x1:.2f} {y1:.2f}")
    d+=f"L{x0+rb:.2f} {y1:.2f}"+(f"A{rb:.2f} {rb:.2f} 0 0 1 {x0:.2f} {y1-rb:.2f}" if rb else f"L{x0:.2f} {y1:.2f}")
    d+=f"L{x0:.2f} {y0+rt:.2f}"+(f"A{rt:.2f} {rt:.2f} 0 0 1 {x0+rt:.2f} {y0:.2f}" if rt else "")
    return d+"Z"
def ear_p(base_out, tip, base_in, rt, bow, grow=1.22):
    (ox,oy),(ix,iy)=base_out,base_in; mx,my=(ox+ix)/2,(oy+iy)/2
    tx,ty=mx+(tip[0]-mx)*grow, my+(tip[1]-my)*grow
    def toward(a,b,d):
        vx,vy=b[0]-a[0],b[1]-a[1]; L=math.hypot(vx,vy); return (a[0]+vx/L*d, a[1]+vy/L*d)
    p1=toward((tx,ty),(ox,oy),rt); p2=toward((tx,ty),(ix,iy),rt)
    def ctrl(a,b):
        cx,cy=(a[0]+b[0])/2,(a[1]+b[1])/2; nx,ny=cx-mx,cy-my; L=math.hypot(nx,ny) or 1
        return (cx+nx/L*bow, cy+ny/L*bow)
    c1=ctrl((ox,oy),p1); c2=ctrl(p2,(ix,iy))
    return f"M{ox:.2f} {oy:.2f}Q{c1[0]:.2f} {c1[1]:.2f} {p1[0]:.2f} {p1[1]:.2f}Q{tx:.2f} {ty:.2f} {p2[0]:.2f} {p2[1]:.2f}Q{c2[0]:.2f} {c2[1]:.2f} {ix:.2f} {iy:.2f}Z"
HEAD=(16,13.6,10.6,8.9); head=ell(*HEAD); face=head+eyes(16,14.8,4.4,3.3)   # hole highlight, as the founder ruled
body=rrect(7.0,21.0,25.0,29.0,2.6,0); tail="M25.0 27.4C29.4 27.6 30.4 23.4 27.4 21.4"
def ears(rt,bow):
    bo=inside((6.8,8.6)); bi=inside((12.8,5.2)); tip=(7.0,2.0)
    return [ear_p(bo,tip,bi,rt,bow), ear_p((32-bo[0],bo[1]),(32-tip[0],tip[1]),(32-bi[0],bi[1]),rt,bow)]
LAD=[('ears-1','sharp-ish',1.6,0.4),('ears-2','soft',2.4,0.8),('ears-3','round (current)',2.9,0.9),('ears-4','rounder',3.6,1.3),('ears-5','very round',4.4,1.8)]
files={}
for k,name,rt,bow in LAD:
    s=svg(face, ears(rt,bow)+[body], tail); open(f'loaf-{k}.svg','w').write(s); files[k]=(name,rt,bow,s)
def inline(s, dark):
    s=re.sub(r'<style>.*?</style>','',s,flags=re.S)
    col='#FFFFFF' if dark else '#0A0A0A'
    s=s.replace('class="m"',f'fill="{col}"').replace('class="s"',f'fill="none" stroke="{col}" stroke-width="3" stroke-linecap="round"')
    return s.replace('<svg ','<svg style="display:block;width:100%;height:100%" ',1)
rows=[]
for k,(name,rt,bow,s) in files.items():
    cells=''.join(f'<div class="c" style="background:#fff"><div style="width:{z}px;height:{z}px">{inline(s,False)}</div><span>{z}</span></div>' for z in (160,64,32,16))
    cells+=''.join(f'<div class="c" style="background:#000;color:#fff"><div style="width:{z}px;height:{z}px">{inline(s,True)}</div><span>{z}</span></div>' for z in (64,32,16))
    rows.append(f'<section><h2><code>{k}</code>{html.escape(name)} <span class="p">tip radius {rt} · edge bow {bow}</span></h2><div class="row">{cells}</div></section>')
page=f'''<title>Infercat Ear Roundness</title><style>
body{{margin:0;padding:24px;background:#F1F2F4;color:#0A0A0A;font:14px/1.45 -apple-system,Helvetica,Arial,sans-serif}}
h1{{font-size:22px;margin:0 0 4px}} .lede{{margin:0 0 14px;color:#5C6068;max-width:78ch}}
section{{background:#fff;border:1px solid #D8DBE0;padding:12px 14px;margin:0 0 12px}} h2{{font-size:15px;margin:0 0 10px}}
code{{font:12px ui-monospace,monospace;background:#EDEFF2;padding:2px 6px;margin-right:8px}} .p{{font:12px ui-monospace,monospace;color:#5C6068;margin-left:10px}}
.row{{display:flex;gap:10px;align-items:flex-end;flex-wrap:wrap}} .c{{display:flex;flex-direction:column;align-items:center;gap:4px;padding:10px;border:1px solid #D8DBE0}} .c span{{font:11px ui-monospace,monospace;opacity:.7}}
</style><h1>The loaf’s ears, five degrees of round</h1><p class="lede">The winner (loaf, rounded top, hole-highlight eyes) with the ears at five roundnesses. Same size and placement throughout; only the tip radius and the outward bow of the edges change. Each at 160, 64, 32 and 16 px on paper and 64, 32, 16 on black.</p>{''.join(rows)}'''
open('ears-founder.html','w').write(page); print('ok')
