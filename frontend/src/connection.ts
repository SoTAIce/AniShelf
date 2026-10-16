import {Capacitor} from '@capacitor/core';

export type Connection={url:string;key:string};
const storageKey='anishelf-server-connection';
export const nativeApp=Capacitor.isNativePlatform();

export function savedConnection():Connection{
 try{
  const value=JSON.parse(localStorage.getItem(storageKey)||'{}') as Partial<Connection>;
  return {url:nativeApp?value.url||'':'',key:value.key||''};
 }catch{return {url:'',key:''}}
}

export function saveConnection(connection:Connection){
 localStorage.setItem(storageKey,JSON.stringify(connection));
}

export function serverURL(raw:string):string{
 const input=raw.trim().replace(/\/+$/,'');
 let parsed:URL;
 try{parsed=new URL(input)}catch{throw new Error('请输入完整的服务器地址，例如 http://192.168.1.10:8787。')}
 if(!['http:','https:'].includes(parsed.protocol)||parsed.username||parsed.password||parsed.pathname!=='/'||parsed.search||parsed.hash){
  throw new Error('请输入完整的服务器地址，例如 http://192.168.1.10:8787');
 }
 if(nativeApp&&['localhost','127.0.0.1','::1'].includes(parsed.hostname)){
  throw new Error('手机里的 localhost 指向手机自身，请填写电脑的局域网地址。');
 }
 const host=parsed.hostname.toLowerCase();
 const localIP=/^(?:10\.|192\.168\.|172\.(?:1[6-9]|2\d|3[01])\.|169\.254\.|100\.(?:6[4-9]|[7-9]\d|1[01]\d|12[0-7])\.)/.test(host);
 if(parsed.protocol==='http:'&&!localIP&&!host.endsWith('.local')){
  throw new Error('非局域网地址请使用 HTTPS，避免泄露连接密钥。');
 }
 return parsed.origin;
}

export function apiURL(path:string):string{
 if(!path.startsWith('/api/'))throw new Error('仅允许访问 AniShelf 接口');
 return (nativeApp?savedConnection().url:'')+path;
}

export function apiFetch(path:string,init:RequestInit={}):Promise<Response>{
 const headers=new Headers(init.headers);
 const key=savedConnection().key;
 if(key)headers.set('X-AniShelf-Key',key);
 return fetch(apiURL(path),{...init,headers});
}

export async function checkConnection(connection:Connection):Promise<void>{
 const url=nativeApp?serverURL(connection.url):'';
 const headers=new Headers();
 if(connection.key)headers.set('X-AniShelf-Key',connection.key);
 const controller=new AbortController();
 const timeout=setTimeout(()=>controller.abort(),8000);
 try{
  const response=await fetch(`${url}/api/health`,{headers,signal:controller.signal,cache:'no-store'});
  if(response.status===401)throw new Error('连接密钥不正确。');
  if(!response.ok)throw new Error(`服务器返回 ${response.status}，请检查地址。`);
  const data=await response.json();
  if(data.status!=='ok')throw new Error('连接的不是 AniShelf 服务。');
 }finally{clearTimeout(timeout)}
}
