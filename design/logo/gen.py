BG="#2C4A6E"; TILE="#FEF8E8"; INK="#0A2E4D"; CORAL="#FC715D"; SHADE="#E9E0CB"
def face(cx,cy,s=1,look=0):
    return f'''<ellipse cx="{cx-120*s+look}" cy="{cy}" rx="{30*s}" ry="{38*s}" fill="{INK}"/><ellipse cx="{cx+120*s+look}" cy="{cy}" rx="{30*s}" ry="{38*s}" fill="{INK}"/>
<path d="M{cx-40*s+look} {cy+30*s} q{40*s} {34*s} {80*s} 0" fill="none" stroke="{INK}" stroke-width="{17*s}" stroke-linecap="round"/>
<ellipse cx="{cx-200*s+look}" cy="{cy+45*s}" rx="{34*s}" ry="{20*s}" fill="{CORAL}" fill-opacity=".28"/><ellipse cx="{cx+200*s+look}" cy="{cy+45*s}" rx="{34*s}" ry="{20*s}" fill="{CORAL}" fill-opacity=".28"/>'''
def svg(body): return f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024"><clipPath id="c"><rect width="1024" height="1024"/></clipPath><g clip-path="url(#c)"><rect width="1024" height="1024" fill="{BG}"/>{body}</g></svg>'
# D: the body IS a load graph -- top edge is a smooth metric curve, coral live cursor at the peak
D=svg(f'''<path d="M-20 600 C 120 600 170 470 290 470 C 400 470 420 560 520 560 C 640 560 680 300 800 300 C 900 300 950 400 1050 380 V1100 H-20 Z" fill="{TILE}"/>
<line x1="800" y1="300" x2="800" y2="1024" stroke="{INK}" stroke-opacity=".10" stroke-width="10" stroke-dasharray="4 26" stroke-linecap="round"/>
<circle cx="800" cy="300" r="62" fill="{CORAL}"/><circle cx="800" cy="300" r="62" fill="none" stroke="{BG}" stroke-width="0"/>
{face(540,800,1.05)}''')
# E: bar-chart family -- the mascot is the tallest bar, two small sleepy bars beside it
E=svg(f'''<g transform="rotate(-6 512 900)">
<rect x="40" y="690" width="210" height="500" rx="70" fill="{SHADE}"/>
<rect x="790" y="560" width="210" height="600" rx="70" fill="{SHADE}"/>
<rect x="300" y="250" width="440" height="950" rx="130" fill="{TILE}"/>
<circle cx="700" cy="250" r="70" fill="{CORAL}"/>
{face(520,620,0.85,15)}
<path d="M110 820 q35 18 70 0 M860 690 q35 18 70 0" fill="none" stroke="{INK}" stroke-opacity=".55" stroke-width="12" stroke-linecap="round"/></g>''')
# F: the inspector -- tile wearing a coral monocle magnifier, watching your servers
F=svg(f'''<g transform="rotate(-11 560 760)"><rect x="70" y="300" width="980" height="1100" rx="210" fill="{TILE}"/>
<ellipse cx="420" cy="700" rx="30" ry="38" fill="{INK}"/>
<circle cx="680" cy="690" r="120" fill="{BG}" fill-opacity=".08" stroke="{CORAL}" stroke-width="40"/>
<circle cx="680" cy="690" r="46" fill="{INK}"/><circle cx="696" cy="672" r="14" fill="{TILE}"/>
<path d="M770 780 L900 930" stroke="{CORAL}" stroke-width="54" stroke-linecap="round"/>
<path d="M500 780 q45 30 90 0" fill="none" stroke="{INK}" stroke-width="17" stroke-linecap="round"/></g>''')
for n,s in [("D-graph",D),("E-bars",E),("F-inspector",F)]: open(n+".svg","w").write(s)
