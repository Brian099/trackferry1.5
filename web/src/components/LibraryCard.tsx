import { useCallback, useEffect, useState } from 'react';
import { api, ApiError, type LibraryStatus } from '../api';
import { Setting } from './Setting';
import { clock, day } from '../copy';
import goldfish from '../assets/brand/goldfish.png';

/** The merged library: how many songs there are and when it last synced. */
export function LibraryCard() {
  const [lib, setLib] = useState<LibraryStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const running = lib?.running ?? false;
  const load = useCallback(async () => {
    try {
      setLib((await api.library()).library);
    } catch (e) {
      setError((e as ApiError).message);
    }
  }, []);
  useEffect(() => {
    load();
    const t = setInterval(() => { if (!document.hidden) load(); }, running ? 1200 : 15000);
    return () => clearInterval(t);
  }, [load, running]);

  const act = async (f: () => Promise<unknown>) => {
    setBusy(true);
    setError(null);
    try {
      await f();
      await load();
    } catch (e) {
      setError((e as ApiError).message);
    } finally {
      setBusy(false);
    }
  };
  const n = (v: number) => v.toLocaleString('zh-CN');
  const last = lib?.last;
  return (
    <Setting id="library" icon={<img className="src-icon" src={goldfish} alt="" draggable={false} />} title="曲库" light={last?.error ? 'bad' : 'ok'} state={running ? '同步中' : lib ? `${n(lib.tracks)} 首` : ''}
      says="飞牛、群晖、Navidrome 和本地文件合并成一份曲库"
      actions={<button className="btn" disabled={running || busy} onClick={() => act(api.scanLibrary)}>{running ? <><span className="spinner spinner-tiny" />正在同步</> : '立即同步'}</button>}>
      {!lib && !error && <span className="spinner" />}
      {lib && (
        <>
          <dl className="tally">
            <div><dd className="num">{n(running ? lib.seen : lib.tracks)}</dd><dt>{running ? '首已读' : '首歌'}</dt></div>
            <div><dd className="num">{n(lib.artists)}</dd><dt>位歌手</dt></div>
            {!running && last?.finished_ms ? <div><dd className="num">{clock(last.finished_ms).slice(0, 5)}</dd><dt>{day(last.finished_ms)}同步</dt></div> : null}
            {!running && last?.skipped ? <div><dd className="num">{n(last.skipped)}</dd><dt>首音箱放不了</dt></div> : null}
          </dl>
          {(last?.roots.length ?? 0) > 1 && <div className="shares" aria-label="目录">{last!.roots.map((x) => <span key={x.name} style={{ flexGrow: x.tracks }}><b>{x.name || '根目录'}</b><small className="num">{n(x.tracks)}</small></span>)}</div>}
          {last?.error && <p className="field-error">{last.error}</p>}
        </>
      )}
      {error && <p className="field-error" role="alert">{error}</p>}
    </Setting>
  );
}
