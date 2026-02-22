export default function RouteNoticePage({ eyebrow = 'Navigation', title, description, ctaLabel, onCta }) {
  return (
    <section className="section route-notice" data-testid="route-notice-page">
      <div className="section-header">
        <p className="eyebrow">{eyebrow}</p>
        <h2>{title}</h2>
        <p>{description}</p>
      </div>
      {ctaLabel && onCta && (
        <div className="cta-row route-notice-actions">
          <button type="button" className="primary" onClick={onCta}>{ctaLabel}</button>
        </div>
      )}
    </section>
  );
}
