import json, re, math
exec(open('gen5.py').read().split("V={}")[0])   # reuse circ/ell/poly/ear/svg/sheetsvg/eyes/ears_soft/inside/STYLE from gen5
def rrect(x0,y0,x1,y1,rt,rb):
    # rectangle with top radius rt and bottom radius rb (0 = square)
    d=f"M{x0+rt:.2f} {y0:.2f}L{x1-rt:.2f} {y0:.2f}"
    d+=f"A{rt:.2f} {rt:.2f} 0 0 1 {x1:.2f} {y0+rt:.2f}" if rt else ""
    d+=f"L{x1:.2f} {y1-rb:.2f}"
    d+=f"A{rb:.2f} {rb:.2f} 0 0 1 {x1-rb:.2f} {y1:.2f}" if rb else f"L{x1:.2f} {y1:.2f}"
    d+=f"L{x0+rb:.2f} {y1:.2f}"
    d+=f"A{rb:.2f} {rb:.2f} 0 0 1 {x0:.2f} {y1-rb:.2f}" if rb else f"L{x0:.2f} {y1:.2f}"
    d+=f"L{x0:.2f} {y0+rt:.2f}"
    d+=f"A{rt:.2f} {rt:.2f} 0 0 1 {x0+rt:.2f} {y0:.2f}" if rt else ""
    return d+"Z"
L={}
HEAD=(16,13.6,10.6,8.9); head=ell(*HEAD); face=head+eyes(16,14.8,4.4,3.3); ears=ears_soft(16, 8.6, 2.0, 6.8, 7.0, 12.8, 5.2)
tail_r="M25.0 27.4C29.4 27.6 30.4 23.4 27.4 21.4"
# A. loaf-round: today's loaf with the two top corners rounded
L['loaf-round']=svg(face, ears+[rrect(7.0,21.0,25.0,29.0,2.6,0)], tail_r)
# B. loaf-soft: all four corners rounded, a softer bun
L['loaf-soft']=svg(face, ears+[rrect(7.0,21.0,25.0,29.0,3.0,2.0)], tail_r)
# C. loaf-wide: the body spreads past the head like a real loaf; rounded top; tail tucked along the right flank as a filled curl
body_w=rrect(4.4,21.4,27.6,29.2,3.4,0)
tail_c="M27.4 28.6C31.2 28.4 31.4 24.2 28.6 22.6"
L['loaf-wide']=svg(face, ears+[body_w], tail_c)
# D. loaf-plain: no tail — the cleanest silhouette at 16 px
L['loaf-plain']=svg(face, ears+[rrect(6.4,21.2,25.6,29.2,3.0,0)])
# E. loaf-squat: head lower (chin resting on the loaf), shorter body, rounded top, tail to the right
HEAD=(16,14.6,10.6,8.9); head2=ell(*HEAD); face2=head2+eyes(16,15.8,4.4,3.3); ears2=ears_soft(16, 9.6, 3.0, 6.8, 7.0, 12.8, 6.2)
L['loaf-squat']=svg(face2, ears2+[rrect(5.6,23.0,26.4,29.6,3.2,0)], "M26.2 28.4C30.2 28.6 31.0 24.8 28.4 23.4")
for k,s in L.items(): open(f'{k}.svg','w').write(s)
rows=[]; y=30; CELL={}
for k,s in L.items():
    inner=sheetsvg(s); cells=[]; x=20
    for size,fg,bg in [(160,'#0A0A0A','#fff'),(64,'#0A0A0A','#fff'),(32,'#0A0A0A','#fff'),(16,'#0A0A0A','#fff'),(64,'#fff','#000'),(32,'#fff','#000'),(16,'#fff','#000'),(64,'#fff','#1F3BFF')]:
        w=size+24
        cells.append(f'<div style="position:absolute;left:{x}px;top:{y}px;width:{w}px;height:{w}px;background:{bg};color:{fg};display:flex;align-items:center;justify-content:center"><div style="width:{size}px;height:{size}px">{inner}</div></div>')
        CELL.setdefault(k,[]).append((size,bg,x+12,y+12)); x+=w+10
    rows.append(f'<div style="position:absolute;left:20px;top:{y-22}px;font:600 13px system-ui">{k}</div>'+''.join(cells)); y+=210
open('loaf-sheet.html','w').write(f'<title>loaf proof</title><style>body{{margin:0;background:#F1F2F4;position:relative;width:1200px;height:{y}px}}</style>'+''.join(rows)); json.dump(CELL,open('loaf-cells.json','w')); print('ok')
