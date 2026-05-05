import { Code2, GitCommitHorizontal, GitPullRequest, Layers3 } from 'lucide-react';

import { Button } from './ui/button.jsx';

const TABS = [
  { id: 'code', label: 'Code', icon: Code2 },
  { id: 'commits', label: 'Commits', icon: GitCommitHorizontal },
  { id: 'changesets', label: 'Changesets', icon: GitPullRequest },
];

export default function SliceDetailNav({
  activeTab,
  sliceId,
  sliceLabel,
  onOpenCode,
  onOpenCommits,
  onOpenChangesets,
}) {
  const handleClick = (tabId) => {
    if (tabId === 'code') {
      onOpenCode?.();
    } else if (tabId === 'commits') {
      onOpenCommits?.();
    } else if (tabId === 'changesets') {
      onOpenChangesets?.();
    }
  };

  return (
    <div className="slice-detail-nav" data-testid="slice-detail-nav">
      <div className="slice-detail-nav-title">
        <span className="slice-detail-nav-icon" aria-hidden="true">
          <Layers3 size={17} />
        </span>
        <span className="slice-detail-nav-copy">
          <span className="slice-detail-nav-name" title={sliceLabel || sliceId}>
            {sliceLabel || sliceId || 'Slice'}
          </span>
          <span className="slice-detail-nav-id" title={sliceId}>
            {sliceId || 'No slice selected'}
          </span>
        </span>
      </div>
      <div className="slice-detail-tabs" role="tablist" aria-label="Slice views">
        {TABS.map((tab) => {
          const Icon = tab.icon;
          const isActive = activeTab === tab.id;
          return (
            <Button
              key={tab.id}
              type="button"
              variant="ghost"
              className={`slice-detail-tab${isActive ? ' active' : ''}`}
              role="tab"
              aria-selected={isActive}
              onClick={() => handleClick(tab.id)}
              data-testid={`slice-detail-tab-${tab.id}`}
            >
              <Icon size={15} aria-hidden="true" />
              {tab.label}
            </Button>
          );
        })}
      </div>
    </div>
  );
}
