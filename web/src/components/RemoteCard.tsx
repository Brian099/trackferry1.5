import { useCallback, useEffect, useState, type FormEvent, type ReactNode } from 'react';
import { ApiError, type RemoteLink } from '../api';
import { Setting, type Light } from './Setting';

/** A music-library source the owner links with an address, user and password. */
export function RemoteCard({ id, title, icon, note, otp = false, get, link, unlink, onLight }: {
  id: string;
  title: string;
  icon: ReactNode;
  note: string;
  otp?: boolean;
  get: () => Promise<RemoteLink>;
  link: (base: string, user: string, password: string, otp: string) => Promise<unknown>;
  unlink: () => Promise<unknown>;
  onLight: (l: Light) => void;
}) {
  const [info, setInfo] = useState<RemoteLink | null>(null);
  const [editing, setEditing] = useState(false);
  const [base, setBase] = useState('');
  const [user, setUser] = useState('');
  const [pw, setPw] = useState('');
  const [otpVal, setOtpVal] = useState('');
  const [busy, setBusy] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      setInfo(await get());
    } catch (e) {
      setError((e as ApiError).message);
    }
  }, [get]);
  useEffect(() => { load(); }, [load]);

  const light: Light | null = !info ? null : info.expired ? 'bad' : info.linked ? 'ok' : 'warn';
  useEffect(() => { if (light) onLight(light); }, [light, onLight]);

  const act = async (f: () => Promise<unknown>) => {
    setBusy(true);
    setError(null);
    try {
      await f();
      await load();
      return true;
    } catch (e) {
      setError((e as ApiError).message);
      return false;
    } finally {
      setBusy(false);
    }
  };
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (await act(() => link(base.trim(), user.trim(), pw, otpVal.trim()))) { setEditing(false); setOtpVal(''); }
    setPw('');
  };

  if (!info) return <Setting id={id} icon={icon} title={title}>{error ? <p className="field-error">{error}</p> : <span className="spinner" />}</Setting>;
  const form = editing || !info.linked || info.expired;
  return (
    <Setting id={id} icon={icon} title={title} light={light ?? 'off'} state={info.expired ? '登录已失效' : info.linked ? '已连接' : '未连接'}
      says={info.linked ? `${info.base}，${info.user}` : note}
      actions={info.linked && !form ? <><button className="btn" onClick={() => setEditing(true)}>换个用户</button><button className="btn btn-quiet" onClick={() => setConfirm(true)}>断开</button></> : undefined}>
      {info.expired && <p className="field-error">登录已失效，请重新登录。</p>}
      {info.linked && !form && (
        <dl className="kv">
          <div><dt>地址</dt><dd className="mono">{info.base}</dd></div>
          <div><dt>用户</dt><dd>{info.user}</dd></div>
        </dl>
      )}
      {confirm && !form && (
        <div className="confirm">
          <p>断开后会删除保存的登录，这个来源的歌不再进入曲库。</p>
          <div className="card-actions"><button className="btn btn-danger" disabled={busy} onClick={async () => { if (await act(unlink)) setConfirm(false); }}>确认断开</button><button className="btn btn-quiet" onClick={() => setConfirm(false)}>取消</button></div>
        </div>
      )}
      {form && (
        <form className="form" onSubmit={submit}>
          <label className="field"><span>地址</span><input value={base} onChange={(e) => setBase(e.target.value)} placeholder={info.base || ''} autoCapitalize="off" spellCheck={false} required /></label>
          <label className="field"><span>用户名</span><input value={user} onChange={(e) => setUser(e.target.value)} autoComplete="off" autoCapitalize="off" spellCheck={false} required /></label>
          <label className="field"><span>密码</span><input type="password" value={pw} onChange={(e) => setPw(e.target.value)} autoComplete="off" required /></label>
          {otp && <label className="field"><span>两步验证码（开了才填）</span><input type="password" value={otpVal} onChange={(e) => setOtpVal(e.target.value)} autoComplete="off" /></label>}
          <div className="card-actions"><button className="btn btn-primary" disabled={busy || !base.trim() || !user.trim() || !pw}>{busy ? <><span className="spinner spinner-tiny" />正在连接</> : '连接'}</button>{editing && <button type="button" className="btn btn-quiet" onClick={() => setEditing(false)}>取消</button>}</div>
          <p className="hint">密码只用于登录和续期，存在这台 NAS 上。</p>
        </form>
      )}
      {error && <p className="field-error" role="alert">{error}</p>}
    </Setting>
  );
}
