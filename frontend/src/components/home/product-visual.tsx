import {
  IconBolt,
  IconCheck,
  IconHomeSignal,
  IconRoute,
  IconShieldCheck,
} from '@tabler/icons-react';
import { BrandMark } from '../brand-mark';
import { appName } from '@/lib/types';

// The hero illustration (handoff_ui.md 8.1): a stylised client drawn in HTML, CSS and inline SVG,
// so it follows both schemes and costs no image request. It is a picture of the product, not
// data: no figures, decorative for assistive technology.
const bars = [34, 52, 41, 68, 57, 80, 63, 46];

export function ProductVisual() {
  return (
    <div className="home-visual" aria-hidden="true">
      <div className="mock-window">
        <div className="mock-bar">
          <i />
          <i />
          <i />
          <span>
            <BrandMark size={16} />
            {appName}
          </span>
        </div>
        <div className="mock-body">
          <div className="mock-status">
            <div className="mock-ring">
              <svg viewBox="0 0 120 120">
                <circle className="mock-ring-track" cx="60" cy="60" r="50" />
                <circle className="mock-ring-arc" cx="60" cy="60" r="50" pathLength="100" />
              </svg>
              <span className="mock-ring-core">
                <IconShieldCheck size={30} stroke={1.6} />
              </span>
              <span className="mock-pulse" />
            </div>
            <div className="mock-status-text">
              <strong>
                <span className="mock-dot" />
                已连接
              </strong>
              <span className="mock-ip">
                <IconHomeSignal size={14} stroke={1.8} />
                住宅原生 IP · 专线
              </span>
              <em>剩余流量</em>
              <div className="mock-meter">
                <span />
              </div>
            </div>
          </div>
          <div className="mock-lines">
            <div className="mock-line active">
              <IconBolt size={16} stroke={1.7} />
              <span>专属线路 · 住宅 IP</span>
              <b className="dedicated">专线</b>
              <IconCheck size={15} className="mock-check" />
            </div>
            <div className="mock-line">
              <IconRoute size={16} stroke={1.7} />
              <span>住宅线路 A</span>
              <b>普通</b>
            </div>
            <div className="mock-line">
              <IconRoute size={16} stroke={1.7} />
              <span>住宅线路 B</span>
              <b>普通</b>
            </div>
          </div>
          <div className="mock-usage">
            <div className="mock-usage-head">
              <span>近 24 小时用量</span>
              <span className="mock-legend">
                <i className="up" />
                上传
                <i className="down" />
                下载
              </span>
            </div>
            <div className="mock-bars">
              {bars.map((height, index) => (
                <span key={index} style={{ height: `${height}%` }}>
                  <i style={{ height: `${28 + ((index * 7) % 18)}%` }} />
                </span>
              ))}
            </div>
          </div>
        </div>
      </div>
      <div className="mock-chip mock-chip-devices">
        <IconHomeSignal size={16} stroke={1.8} />
        住宅原生 IP
      </div>
      <div className="mock-chip mock-chip-rate">
        <IconBolt size={16} stroke={1.8} />
        专线稳定连接
      </div>
    </div>
  );
}
