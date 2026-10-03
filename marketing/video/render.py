import json, subprocess, sys, time, os, base64, urllib.request, shutil
from websocket import create_connection
CH="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
PORT=9344
def start():
    p=subprocess.Popen([CH,"--headless=new","--disable-gpu","--hide-scrollbars","--no-first-run","--mute-audio",f"--remote-debugging-port={PORT}","--remote-allow-origins=*","--user-data-dir=/tmp/wbi-vid-profile","--allow-file-access-from-files","about:blank"],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    for _ in range(60):
        try:
            tabs=json.load(urllib.request.urlopen(f"http://127.0.0.1:{PORT}/json"))
            page=[t for t in tabs if t["type"]=="page"][0]; return p,page["webSocketDebuggerUrl"]
        except Exception: time.sleep(.25)
    raise SystemExit("chrome did not start")
class CDP:
    def __init__(s,url): s.ws=create_connection(url,max_size=None); s.i=0
    def call(s,m,**p):
        s.i+=1; s.ws.send(json.dumps({"id":s.i,"method":m,"params":p}))
        while True:
            r=json.loads(s.ws.recv())
            if r.get("id")==s.i:
                if "error" in r: raise RuntimeError(r["error"])
                return r.get("result",{})
def main(times=None, outdir="frames", fps=30, total=26.0, scale=2, fmt="png"):
    p,url=start()
    try:
        c=CDP(url)
        c.call("Page.enable"); c.call("Runtime.enable")
        c.call("Emulation.setDeviceMetricsOverride",width=540,height=960,deviceScaleFactor=scale,mobile=False)
        c.call("Page.navigate",url="file://"+os.path.abspath(os.path.join(os.path.dirname(__file__),"scene.html"))); time.sleep(1.2)
        errs=c.call("Runtime.evaluate",expression="typeof setT")["result"]["value"]
        assert errs=="function","scene did not load"
        shutil.rmtree(outdir,ignore_errors=True); os.makedirs(outdir)
        ts=times if times is not None else [i/fps for i in range(int(total*fps))]
        t0=time.time()
        for n,t in enumerate(ts):
            r=c.call("Runtime.evaluate",expression=f"setT({t})")
            if "exceptionDetails" in r: raise SystemExit("JS error at t=%s: %s"%(t,json.dumps(r["exceptionDetails"])[:400]))
            shot=c.call("Page.captureScreenshot",format=fmt,**({"quality":95} if fmt=="jpeg" else {}))
            name=f"{outdir}/f_{n:04d}.{fmt}" if times is None else f"{outdir}/t_{t:05.2f}.{fmt}"
            open(name,"wb").write(base64.b64decode(shot["data"]))
            if n%60==0: print(f"{n}/{len(ts)} ({time.time()-t0:.0f}s)",flush=True)
    finally:
        p.terminate()
if __name__=="__main__":
    if len(sys.argv)>1 and sys.argv[1]=="stills": main(times=[float(x) for x in sys.argv[2:]],outdir="stills",scale=1)
    else: main()
