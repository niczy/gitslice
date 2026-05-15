import { useEffect, useMemo, useRef, useState } from 'react';
import { Bot, Check, Code2, Copy, GitCommitHorizontal, GitPullRequest, Layers3, Settings } from 'lucide-react';

import { copyToClipboard } from '../utils/clipboard.js';
import { buildGitCloneCommand, buildGitEndpoint, buildSliceCheckoutCommand } from '../utils/git.js';
import { Button } from './ui/button.jsx';

const TABS = [
  { id: 'code', label: 'Code', icon: Code2 },
  { id: 'commits', label: 'Commits', icon: GitCommitHorizontal },
  { id: 'changesets', label: 'Changesets', icon: GitPullRequest },
  { id: 'agents', label: 'Agents', icon: Bot },
  { id: 'settings', label: 'Settings', icon: Settings },
];

export default function SliceDetailNav({
  activeTab,
  sliceId,
  sliceLabel,
  slice = null,
  publicApiBaseUrl = '',
  onOpenCode,
  onOpenCommits,
  onOpenChangesets,
  onOpenAgents,
  onOpenSettings,
}) {
  const [isGetCodeOpen, setIsGetCodeOpen] = useState(false);
  const [copyState, setCopyState] = useState('');
  const getCodeRef = useRef(null);
  const gitEndpoint = useMemo(
    () => buildGitEndpoint({ slice, publicApiBaseUrl }),
    [publicApiBaseUrl, slice],
  );
  const cloneCommand = useMemo(() => buildGitCloneCommand(gitEndpoint), [gitEndpoint]);
  const checkoutCommand = useMemo(
    () => buildSliceCheckoutCommand({ slice, sliceId }),
    [slice, sliceId],
  );
  const visibleTabs = useMemo(() => (
    TABS.filter((tab) => tab.id !== 'settings' || (!slice?.is_root && onOpenSettings))
  ), [onOpenSettings, slice?.is_root]);

  const handleClick = (tabId) => {
    if (tabId === 'code') {
      onOpenCode?.();
    } else if (tabId === 'commits') {
      onOpenCommits?.();
    } else if (tabId === 'changesets') {
      onOpenChangesets?.();
    } else if (tabId === 'agents') {
      onOpenAgents?.();
    } else if (tabId === 'settings') {
      onOpenSettings?.();
    }
  };

  useEffect(() => {
    if (!isGetCodeOpen) {
      return undefined;
    }

    const handleDocumentClick = (event) => {
      if (getCodeRef.current?.contains(event.target)) {
        return;
      }
      setIsGetCodeOpen(false);
    };
    const handleKeyDown = (event) => {
      if (event.key === 'Escape') {
        setIsGetCodeOpen(false);
      }
    };

    document.addEventListener('mousedown', handleDocumentClick);
    document.addEventListener('keydown', handleKeyDown);
    return () => {
      document.removeEventListener('mousedown', handleDocumentClick);
      document.removeEventListener('keydown', handleKeyDown);
    };
  }, [isGetCodeOpen]);

  useEffect(() => {
    if (!copyState) {
      return undefined;
    }
    const timer = window.setTimeout(() => setCopyState(''), 1800);
    return () => window.clearTimeout(timer);
  }, [copyState]);

  const copyCommand = async (target, command) => {
    if (!command) {
      return;
    }
    try {
      await copyToClipboard(command);
      setCopyState(target);
    } catch {
      setCopyState('error');
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
        </span>
        <div className="slice-detail-get-code" ref={getCodeRef}>
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="slice-detail-get-code-button"
            onClick={() => setIsGetCodeOpen((open) => !open)}
            aria-expanded={isGetCodeOpen}
            aria-haspopup="dialog"
            data-testid="slice-get-code-button"
          >
            <Code2 size={15} aria-hidden="true" />
            Get Code
          </Button>
          {isGetCodeOpen && (
            <div className="slice-detail-get-code-popover" role="dialog" aria-label="Get slice code">
              <div className="slice-detail-get-code-header">
                <span>Get this slice locally</span>
              </div>
              <div className="slice-detail-command-row">
                <code data-testid="slice-get-code-command">
                  {checkoutCommand || 'Slice checkout unavailable'}
                </code>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  className="slice-detail-copy-command"
                  onClick={() => copyCommand('checkout', checkoutCommand)}
                  disabled={!checkoutCommand}
                  aria-label="Copy checkout command"
                  title="Copy checkout command"
                  data-testid="slice-get-code-copy"
                >
                  {copyState === 'checkout' ? <Check size={16} aria-hidden="true" /> : <Copy size={16} aria-hidden="true" />}
                </Button>
              </div>
              <div className="slice-detail-get-code-secondary">
                <span>Git clone</span>
                <div className="slice-detail-command-row">
                  <code data-testid="slice-get-code-git-command">
                    {cloneCommand || 'Git endpoint unavailable'}
                  </code>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    className="slice-detail-copy-command"
                    onClick={() => copyCommand('git', cloneCommand)}
                    disabled={!cloneCommand}
                    aria-label="Copy Git clone command"
                    title="Copy Git clone command"
                    data-testid="slice-get-code-git-copy"
                  >
                    {copyState === 'git' ? <Check size={16} aria-hidden="true" /> : <Copy size={16} aria-hidden="true" />}
                  </Button>
                </div>
              </div>
              <p className={`slice-detail-get-code-note${copyState === 'error' ? ' error' : ''}`} aria-live="polite">
                {copyState === 'checkout'
                  ? 'Copied checkout command.'
                  : copyState === 'git'
                    ? 'Copied Git clone command.'
                    : copyState === 'error'
                    ? 'Unable to copy command.'
                    : 'Run gs login first for private slices.'}
              </p>
            </div>
          )}
        </div>
      </div>
      <div className="slice-detail-tabs" role="tablist" aria-label="Slice views">
        {visibleTabs.map((tab) => {
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
