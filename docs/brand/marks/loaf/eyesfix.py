import json, re
exec(open('gen5.py').read().split("V={}")[0])
exec(open('loaf.py').read().split("L={}")[0].split("exec(")[0])  # nothing needed; rrect defined below
def rrect(x0,y0,x1,y1,rt,rb):
    d=f"M{x0+rt:.2f} {y0:.2f}L{x1-rt:.2f} {y0:.2f}"+(f"A{rt:.2f} {rt:.2f} 0 0 1 {x1:.2f} {y0+rt:.2f}" if rt else "")
    d+=f"L{x1:.2f} {y1-rb:.2f}"+(f"A{rb:.2f} {rb:.2f} 0 0 1 {x1-rb:.2f} {y1:.2f}" if rb else f"L{x1:.2f} {y1:.2f}")
    d+=f"L{x0+rb:.2f} {y1:.2f}"+(f"A{rb:.2f} {rb:.2f} 0 0 1 {x0:.2f} {y1-rb:.2f}" if rb else f"L{x0:.2f} {y1:.2f}")
    d+=f"L{x0:.2f} {y0+rt:.2f}"+(f"A{rt:.2f} {rt:.2f} 0 0 1 {x0+rt:.2f} {y0:.2f}" if rt else "")
    return d+"Z"
STYLE2 = "<style>.m{fill:#0A0A0A}.h{fill:#FFFFFF}.s{fill:none;stroke:#0A0A0A;stroke-width:3;stroke-linecap:round}@media (prefers-color-scheme: dark){.m{fill:#FFFFFF}.h{display:none}.s{stroke:#FFFFFF}}</style>"
def eyes_nohl(cx,cy,dx,r,k_p):
    d=""
    for sx in (-1,1):
        ex=cx+sx*dx; d+=circ(ex,cy,r)+circ(ex,cy,r*k_p)
    return d
def hl_circles(cx,cy,dx,r,k_h):
    return "".join(f'<circle class="h" cx="{cx+sx*dx-r*k_h:.2f}" cy="{cy-r*k_h:.2f}" r="{r*k_h:.2f}"/>' for sx in (-1,1))
HEAD=(16,13.6,10.6,8.9); head=ell(*HEAD); ears=ears_soft(16, 8.6, 2.0, 6.8, 7.0, 12.8, 5.2)
body=rrect(7.0,21.0,25.0,29.0,2.6,0); tail="M25.0 27.4C29.4 27.6 30.4 23.4 27.4 21.4"
EX,EY,DX,R=16,14.8,4.4,3.3
def build(face, extra="", style=STYLE):
    parts=[f'<path class="m" fill-rule="evenodd" d="{face}"/>', f'<path class="m" d="{" ".join(ears+[body])}"/>', f'<path class="s" d="{tail}"/>', extra]
    return f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32" role="img" aria-label="Infercat">{style}{"".join(parts)}</svg>'
E={}
# A. as shipped on the sheet: highlight as a hole (inverts into the sideways glance)
E['hole-highlight']=build(head+eyes(EX,EY,DX,R))
# B. highlight on paper only: a paper-coloured dot over the pupil, hidden when the mark is light-on-dark
E['paper-highlight']=build(head+eyes_nohl(EX,EY,DX,R,0.72), hl_circles(EX,EY,DX,R,0.36), STYLE2)
# C. no highlight, pupil 0.72
E['plain-72']=build(head+eyes_nohl(EX,EY,DX,R,0.72))
# D. no highlight, pupil 0.80 (a rounder, darker eye)
E['plain-80']=build(head+eyes_nohl(EX,EY,DX,R,0.80))
for k,s in E.items(): open(f'loaf-round-{k}.svg','w').write(s)
# sheet: light cells use the file's own light colours; dark cells emulate the dark media query by hand
def inline(s, dark):
    s=re.sub(r'<style>.*?</style>','',s,flags=re.S)
    if dark:
        s=s.replace('class="m"','fill="#FFFFFF"').replace('class="s"','fill="none" stroke="#FFFFFF" stroke-width="3" stroke-linecap="round"')
        s=re.sub(r'<circle class="h"[^>]*/>','',s)
    else:
        s=s.replace('class="m"','fill="#0A0A0A"').replace('class="h"','fill="#FFFFFF"').replace('class="s"','fill="none" stroke="#0A0A0A" stroke-width="3" stroke-linecap="round"')
    return s.replace('<svg ','<svg style="display:block;width:100%;height:100%" ',1)
rows=[]; y=30
for k,s in E.items():
    cells=[]; x=20
    for size,dark,bg in [(160,False,'#fff'),(64,False,'#fff'),(32,False,'#fff'),(16,False,'#fff'),(160,True,'#000'),(64,True,'#000'),(32,True,'#000'),(16,True,'#000')]:
        w=size+24
        cells.append(f'<div style="position:absolute;left:{x}px;top:{y}px;width:{w}px;height:{w}px;background:{bg};display:flex;align-items:center;justify-content:center"><div style="width:{size}px;height:{size}px">{inline(s,dark)}</div></div>')
        x+=w+10
    rows.append(f'<div style="position:absolute;left:20px;top:{y-22}px;font:600 13px system-ui">{k}</div>'+''.join(cells)); y+=210
open('eyes-sheet.html','w').write(f'<title>eyes proof</title><style>body{{margin:0;background:#F1F2F4;position:relative;width:1200px;height:{y}px}}</style>'+''.join(rows)); print('ok')
