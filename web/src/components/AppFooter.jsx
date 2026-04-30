export default function AppFooter({ docsUrl, statusUrl, githubUrl }) {
  return (
    <>
      <footer className="footer bg-card/70" aria-label="Global footer">
        <p className="footer-copy">Git Slice</p>
        <nav className="footer-links" aria-label="Self-service links">
          <a href={docsUrl}>Docs</a>
          <a href={statusUrl} target="_blank" rel="noreferrer">Status</a>
          <a href={githubUrl} target="_blank" rel="noreferrer">GitHub</a>
        </nav>
      </footer>
    </>
  );
}
