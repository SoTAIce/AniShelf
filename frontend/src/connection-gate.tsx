import {useEffect,useState,type FormEvent,type ReactNode} from 'react';
import {ArrowRight,BookmarkSimple,WifiHigh} from '@phosphor-icons/react';
import {checkConnection,nativeApp,savedConnection,saveConnection,serverURL,type Connection} from './connection';

export function ConnectionGate({children}:{children:ReactNode}){
 const [connection,setConnection]=useState<Connection>(savedConnection);
 const [state,setState]=useState<'checking'|'ready'|'setup'>('checking');
 const [error,setError]=useState('');
 const [busy,setBusy]=useState(false);
 const [canReturn,setCanReturn]=useState(false);
 useEffect(()=>{
  let active=true;
  if(nativeApp&&!connection.url){setState('setup');return}
  checkConnection(connection).then(()=>{if(active){setCanReturn(true);setState('ready')}}).catch(e=>{
   if(active){setError(e instanceof Error?e.message:'连接失败，请检查电脑和手机的网络。');setState('setup')}
  });
  return()=>{active=false};
 },[]);
 useEffect(()=>{
  const open=()=>{setConnection(savedConnection());setError('');setState('setup')};
  window.addEventListener('anishelf:connection-settings',open);
  return()=>window.removeEventListener('anishelf:connection-settings',open);
 },[]);
 async function connect(event:FormEvent){
  event.preventDefault();setBusy(true);setError('');
  try{
   const next={url:nativeApp?serverURL(connection.url):'',key:connection.key.trim()};
   await checkConnection(next);
   saveConnection(next);
   setCanReturn(true);setState('ready');
  }catch(e){setError(e instanceof Error?e.message:'连接失败，请检查电脑和手机的网络。')}
  finally{setBusy(false)}
 }
 if(state==='ready')return <>{children}</>;
 if(state==='checking')return <div className="connection-screen"><div className="connection-loading">正在连接 AniShelf…</div></div>;
 return <main className="connection-screen"><section className="connection-card"><div className="connection-mark"><BookmarkSimple weight="fill" size={24}/></div><span className="eyebrow">ANISHELF · 连接设置</span><h1>{nativeApp?'在手机上，继续追番。':'连接你的番选服务'}</h1><p>{nativeApp?'先让电脑和手机连接同一个 Wi-Fi，并在电脑上启动 AniShelf 局域网服务。':'请输入电脑端设置的连接密钥；如果服务未启动，请先在电脑上运行它。'}</p><form onSubmit={connect}>{nativeApp&&<label>电脑端地址<input type="url" inputMode="url" autoCapitalize="none" autoCorrect="off" value={connection.url} onChange={e=>setConnection({...connection,url:e.target.value})} placeholder="http://192.168.1.10:8787" required/></label>}<label>连接密钥<input type="password" value={connection.key} onChange={e=>setConnection({...connection,key:e.target.value})} placeholder="电脑端设置的连接密钥" autoComplete="off"/></label>{error&&<p className="connection-error" role="alert">{error}</p>}<button className="primary" disabled={busy} type="submit"><WifiHigh size={19}/>{busy?'正在验证…':'连接并进入'}<ArrowRight size={16}/></button>{canReturn&&<button className="connection-back" type="button" onClick={()=>setState('ready')}>返回番选</button>}</form><div className="connection-hint">{nativeApp?'电脑关闭或离开同一网络后，手机应用将无法读取片库和在线播放。':'连接密钥只保存在当前设备的应用存储中。'}</div></section></main>;
}
