import {
  AGENTS_SIDEBAR_MAX_WIDTH,
  AGENTS_SIDEBAR_MIN_WIDTH,
} from '../../features/agents/agentConstants.js';

export default function SliceAgentsResizeHandle({
  agentsSidebarWidth,
  onDoubleClick,
  onKeyDown,
  onPointerDown,
}) {
  return (
    <div
      className="slice-agents-resize-handle"
      role="separator"
      aria-label="Resize agents and conversations panel"
      aria-orientation="vertical"
      aria-valuemin={AGENTS_SIDEBAR_MIN_WIDTH}
      aria-valuemax={AGENTS_SIDEBAR_MAX_WIDTH}
      aria-valuenow={Math.round(agentsSidebarWidth)}
      tabIndex={0}
      title="Drag to resize. Double-click to reset."
      data-testid="slice-agents-resize-handle"
      onPointerDown={onPointerDown}
      onKeyDown={onKeyDown}
      onDoubleClick={onDoubleClick}
    >
      <span aria-hidden="true" />
    </div>
  );
}
