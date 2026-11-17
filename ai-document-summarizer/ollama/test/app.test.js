import {test} from 'node:test';
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {spawn} from 'node:child_process';
import {mkdtempSync,rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';

test('document upload and cited retrieval work end to end',async()=>{
 const mock=createServer(async(req,res)=>{let body='';for await(const c of req)body+=c;const input=body ? JSON.parse(body) : {}; res.setHeader('content-type','application/json');if(req.url==='/api/embed')res.end(JSON.stringify({embeddings:input.input.map(()=>[1,0])}));else res.end(JSON.stringify({message:{content:'Manager approval is required [1].'}}));});
 await new Promise(r=>mock.listen(0,'127.0.0.1',r));const mockPort=mock.address().port;const dir=mkdtempSync(join(tmpdir(),'doc-summarizer-'));const port=31000+Math.floor(Math.random()*10000);const child=spawn(process.execPath,['server.js'],{env:{...process.env,PORT:String(port),DATA_DIR:dir,OLLAMA_BASE_URL:`http://127.0.0.1:${mockPort}`},stdio:'ignore'});const base=`http://127.0.0.1:${port}`;
 try{let ready=false;for(let i=0;i<60;i++){try{await fetch(base+'/api/status');ready=true;break}catch{await new Promise(r=>setTimeout(r,100))}}assert.ok(ready,'server started');const upload=async(path,content)=>{const f=new FormData();f.append('file',new Blob([content],{type:'text/plain'}),'sample.txt');const r=await fetch(base+path,{method:'POST',body:f});assert.equal(r.status,200,await r.clone().text());return r.json()};await upload('/api/documents','Manager approval is required for purchases.');let r=await fetch(base+'/api/ask',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({question:'Who approves?'})});let data=await r.json();assert.match(data.answer,/\[1\]/);assert.equal(data.sources[0].page,1);
 }finally{child.kill();await new Promise(r=>child.once('exit',r));await new Promise(r=>mock.close(r));rmSync(dir,{recursive:true,force:true})}
});


