import json
def circ(cx,cy,r): return f"M{cx-r:.2f} {cy:.2f}A{r:.2f} {r:.2f} 0 1 0 {cx+r:.2f} {cy:.2f}A{r:.2f} {r:.2f} 0 1 0 {cx-r:.2f} {cy:.2f}Z"
def ell(cx,cy,rx,ry): return f"M{cx-rx:.2f} {cy:.2f}A{rx:.2f} {ry:.2f} 0 1 0 {cx+rx:.2f} {cy:.2f}A{rx:.2f} {ry:.2f} 0 1 0 {cx-rx:.2f} {cy:.2f}Z"
def poly(pts): return "M"+"L".join(f"{x:.2f} {y:.2f}" for x,y in pts)+"Z"
def ear(base_out, tip, base_in, rt=2.0):
    # a triangle with a rounded tip: replace the tip vertex by a short arc
    (ox,oy),(tx,ty),(ix,iy)=base_out,tip,base_in
    import math
    def toward(a,b,d):
        vx,vy=b[0]-a[0],b[1]-a[1]; L=math.hypot(vx,vy); return (a[0]+vx/L*d, a[1]+vy/L*d)
    p1=toward(tip,base_out,rt); p2=toward(tip,base_in,rt)
    return f"M{ox:.2f} {oy:.2f}L{p1[0]:.2f} {p1[1]:.2f}Q{tx:.2f} {ty:.2f} {p2[0]:.2f} {p2[1]:.2f}L{ix:.2f} {iy:.2f}Z"
STYLE = "<style>.m{fill:#0A0A0A}.s{fill:none;stroke:#0A0A0A;stroke-width:3;stroke-linecap:round}@media (prefers-color-scheme: dark){.m{fill:#FFFFFF}.s{stroke:#FFFFFF}}</style>"
def svg(face, solids, tail=None):
    parts=[f'<path class="m" fill-rule="evenodd" d="{face}"/>']
    if solids: parts.append(f'<path class="m" d="{" ".join(solids)}"/>')
    if tail: parts.append(f'<path class="s" d="{tail}"/>')
    return f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32" role="img" aria-label="Infercat">{STYLE}{"".join(parts)}</svg>'
def sheetsvg(s):
    s=s.replace(STYLE,'').replace('class="m"','fill="currentColor"').replace('class="s"','fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round"')
    return s.replace('<svg ','<svg style="display:block;width:100%;height:100%" ',1)
def eyes(cx, cy, dx, r, k_p=0.72, k_h=0.36):
    d=""
    for sx in (-1,1):
        ex=cx+sx*dx; d+=circ(ex,cy,r)+circ(ex,cy,r*k_p)
        if k_h>0: d+=circ(ex-r*k_h, cy-r*k_h, r*k_h)
    return d
HEAD=None  # (cx, cy, rx, ry) of the head the ears attach to; set before calling ears_soft
def inside(p, k=0.86):
    cx,cy,rx,ry=HEAD; return (cx+(p[0]-cx)*k, cy+(p[1]-cy)*k)
def ears_soft(cx, y_out, y_tip, x_out, x_tip, x_in, y_in):
    bo=inside((x_out,y_out)); bi=inside((x_in,y_in))
    L=ear(bo,(x_tip,y_tip),bi); R=ear((2*cx-bo[0],bo[1]),(2*cx-x_tip,y_tip),(2*cx-bi[0],bi[1])); return [L,R]
V={}
# face: shorter, rounder ears (the reference's), wide head, big low eyes, pupils with a highlight
HEAD=(16,16.4,11.2,9.4)
head=ell(16,16.4,11.2,9.4)
V['face']=svg(head+eyes(16,17.6,4.7,3.6), ears_soft(16, 11.2, 4.4, 6.2, 6.4, 12.6, 7.6))
# face-sharp: the same with the earlier taller, pointed ears (for comparison)
V['face-sharp']=svg(head+eyes(16,17.6,4.7,3.6), [poly([inside((6.0,11.0)),(5.2,3.2),inside((12.4,7.4))]), poly([inside((26.0,11.0)),(26.8,3.2),inside((19.6,7.4))])])
# peek: head over an edge, two ink paws hanging over a thin rule (the reference's pose)
HEAD=(16,14.8,11.2,9.4)
head=ell(16,14.8,11.2,9.4)
face=head+eyes(16,16.0,4.7,3.6)
paws=circ(10.4,24.6,2.7)+circ(21.6,24.6,2.7)
rule=poly([(3.4,27.9),(28.6,27.9),(28.6,29.6),(3.4,29.6)])
V['peek']=svg(face, ears_soft(16, 9.6, 2.8, 6.2, 6.4, 12.6, 6.0)+[paws, rule])
# loaf: face over a small loaf with the tail
HEAD=(16,13.6,10.6,8.9)
head=ell(16,13.6,10.6,8.9)
V['loaf']=svg(head+eyes(16,14.8,4.4,3.3), ears_soft(16, 8.6, 2.0, 6.8, 7.0, 12.8, 5.2)+[poly([(7.0,21.0),(25.0,21.0),(25.0,29.0),(7.0,29.0)])], "M25.0 27.4C29.4 27.6 30.4 23.4 27.4 21.4")
# seated: the Swiss body, enlarged head, soft ears, reference-proportion eyes
HEAD=(12.8,12.4,8.9,8.9)
V['seated']=svg(circ(12.8,12.4,8.9)+eyes(12.8,13.9,3.6,3.05), ears_soft(12.8, 7.8, 1.8, 5.6, 5.4, 11.0, 5.0)+[poly([(12.8,16.0),(22.4,29.2),(3.2,29.2)])], "M20.4 27.6C26.8 29.0 29.2 24.4 26.2 20.8")
for k,s in V.items(): open(f'{k}.svg','w').write(s)
rows=[]; y=30; CELL={}
for k,s in V.items():
    inner=sheetsvg(s); cells=[]; x=20
    for size,fg,bg in [(160,'#0A0A0A','#fff'),(64,'#0A0A0A','#fff'),(32,'#0A0A0A','#fff'),(16,'#0A0A0A','#fff'),(64,'#fff','#000'),(32,'#fff','#000'),(16,'#fff','#000'),(64,'#fff','#1F3BFF')]:
        w=size+24
        cells.append(f'<div style="position:absolute;left:{x}px;top:{y}px;width:{w}px;height:{w}px;background:{bg};color:{fg};display:flex;align-items:center;justify-content:center"><div style="width:{size}px;height:{size}px">{inner}</div></div>')
        CELL.setdefault(k,[]).append((size,bg,x+12,y+12)); x+=w+10
    rows.append(f'<div style="position:absolute;left:20px;top:{y-22}px;font:600 13px system-ui">{k}</div>'+''.join(cells)); y+=210
open('sheet.html','w').write(f'<title>kawaii proof</title><style>body{{margin:0;background:#F1F2F4;position:relative;width:1200px;height:{y}px}}</style>'+''.join(rows)); json.dump(CELL,open('cells.json','w')); print('ok')
