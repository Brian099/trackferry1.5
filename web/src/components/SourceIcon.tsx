import type { LayerName } from '../api';
import goldfish from '../assets/brand/goldfish.png';
import kuwo from '../assets/sources/kuwo.jpg';
import kugou from '../assets/sources/kugou.jpg';
import migu from '../assets/sources/migu.jpg';
import netease from '../assets/sources/netease.jpg';
import xiaoai from '../assets/sources/xiaoai.jpg';

const src: Record<LayerName, string> = { library: goldfish, kuwo, kugou, migu, netease, xiaomi: xiaoai };
export function SourceIcon({ name }: { name: LayerName }) {
  return <img className="src-icon" src={src[name]} alt="" draggable={false} />;
}
