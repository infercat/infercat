import re, math, html
exec(open('gen5.py').read().split("V={}")[0])
def rrect(x0,y0,x1,y1,rt,rb):
    d=f"M{x0+rt:.2f} {y0:.2f}L{x1-rt:.2f} {y0:.2f}"+(f"A{rt:.2f} {rt:.2f} 0 0 1 {x1:.2f} {y0+rt:.2f}" if rt else "")
    d+=f"L{x1:.2f} {y1-rb:.2f}"+(f"A{rb:.2f} {rb:.2f} 0 0 1 {x1-rb:.2f} {y1:.2f}" if rb else f"L{x1:.2f} {y1:.2f}")
    d+=f"L{x0+rb:.2f} {y1:.2f}"+(f"A{rb:.2f} {rb:.2f} 0 0 1 {x0:.2f} {y1-rb:.2f}" if rb else f"L{x0:.2f} {y1:.2f}")
    d+=f"L{x0:.2f} {y0+rt:.2f}"+(f"A{rt:.2f} {rt:.2f} 0 0 1 {x0+rt:.2f} {y0:.2f}" if rt else "")
    return d+"Z"
def ear_one(B1, T, B2, k, bow=0.6, grow=1.22):
    # one continuous curve per side, meeting at the apex with a shared tangent (perpendicular to the ear axis):
    # side 1 = cubic B1 -> T, side 2 = cubic T -> B2. `k` = apex tangent length (roundness); `bow` = outward belly.
    mx,my=(B1[0]+B2[0])/2,(B1[1]+B2[1])/2
    T=(mx+(T[0]-mx)*grow, my+(T[1]-my)*grow)
    ax,ay=T[0]-mx,T[1]-my; L=math.hypot(ax,ay); ax,ay=ax/L,ay/L          # ear axis, base -> apex
    px,py=-ay,ax                                                          # perpendicular to the axis
    # which way is "outward" for each side: side 1 is toward B1, side 2 toward B2
    s1=1 if (B1[0]-mx)*px+(B1[1]-my)*py>0 else -1; s2=-s1
    def side(P, s):
        # first control: from the base, along the base->apex direction plus an outward belly
        c1=(P[0]+(T[0]-P[0])*0.45+s*px*bow, P[1]+(T[1]-P[1])*0.45+s*py*bow)
        c2=(T[0]+s*px*k, T[1]+s*py*k)                                     # apex tangent, perpendicular to the axis
        return c1,c2
    a1,a2=side(B1,s1); b1,b2=side(B2,s2)
    return (f"M{B1[0]:.2f} {B1[1]:.2f}C{a1[0]:.2f} {a1[1]:.2f} {a2[0]:.2f} {a2[1]:.2f} {T[0]:.2f} {T[1]:.2f}"
            f"C{b2[0]:.2f} {b2[1]:.2f} {b1[0]:.2f} {b1[1]:.2f} {B2[0]:.2f} {B2[1]:.2f}Z")
HEAD=(16,13.6,10.6,8.9); head=ell(*HEAD); face=head+eyes(16,14.8,4.4,3.3)
body=rrect(7.0,21.0,25.0,29.0,2.6,0); tail="M25.0 27.4C29.4 27.6 30.4 23.4 27.4 21.4"
def ears(k,bow):
    bo=inside((6.8,8.6)); bi=inside((12.8,5.2)); tip=(7.0,2.0)
    return [ear_one(bo,tip,bi,k,bow), ear_one((32-bo[0],bo[1]),(32-tip[0],tip[1]),(32-bi[0],bi[1]),k,bow)]
LAD=[('one-1','pointed',0.9,0.4),('one-2','soft',1.6,0.6),('one-3','round',2.3,0.8),('one-4','very round',3.0,1.0)]
files={}
for k,name,kk,bow in LAD:
    s=svg(face, ears(kk,bow)+[body], tail); open(f'loaf-{k}.svg','w').write(s); files[k]=(name,kk,bow,s)
def inline(s, dark):
    s=re.sub(r'<style>.*?</style>','',s,flags=re.S); col='#FFFFFF' if dark else '#0A0A0A'
    s=s.replace('class="m"',f'fill="{col}"').replace('class="s"',f'fill="none" stroke="{col}" stroke-width="3" stroke-linecap="round"')
    return s.replace('<svg ','<svg style="display:block;width:100%;height:100%" ',1)
rows=[]
for k,(name,kk,bow,s) in files.items():
    cells=''.join(f'<div class="c" style="background:#fff"><div style="width:{z}px;height:{z}px">{inline(s,False)}</div><span>{z}</span></div>' for z in (240,160,64,32,16))
    cells+=''.join(f'<div class="c" style="background:#000;color:#fff"><div style="width:{z}px;height:{z}px">{inline(s,True)}</div><span>{z}</span></div>' for z in (64,32,16))
    rows.append(f'<section><h2><code>{k}</code>{html.escape(name)} <span class="p">apex tangent {kk} · belly {bow}</span></h2><div class="row">{cells}</div></section>')
page=f'''<title>Infercat Ears, One Curve</title><style>
body{{margin:0;padding:24px;background:#F1F2F4;color:#0A0A0A;font:14px/1.45 -apple-system,Helvetica,Arial,sans-serif}}
h1{{font-size:22px;margin:0 0 4px}} .lede{{margin:0 0 14px;color:#5C6068;max-width:78ch}}
section{{background:#fff;border:1px solid #D8DBE0;padding:12px 14px;margin:0 0 12px}} h2{{font-size:15px;margin:0 0 10px}}
code{{font:12px ui-monospace,monospace;background:#EDEFF2;padding:2px 6px;margin-right:8px}} .p{{font:12px ui-monospace,monospace;color:#5C6068;margin-left:10px}}
.row{{display:flex;gap:10px;align-items:flex-end;flex-wrap:wrap}} .c{{display:flex;flex-direction:column;align-items:center;gap:4px;padding:10px;border:1px solid #D8DBE0}} .c span{{font:11px ui-monospace,monospace;opacity:.7}}
</style><h1>The loaf’s ears, one curve</h1><p class="lede">Each ear is now two continuous curves, one per side, meeting at the apex with a shared tangent, so there is no straight run and no separate tip arc. Roundness is a single number: how far the apex tangents extend. Four settings, at 240, 160, 64, 32 and 16 px on paper and 64, 32, 16 on black.</p>{''.join(rows)}'''
open('ears1-founder.html','w').write(page); print('ok')
