# Portfolio Website

A professional, single-page portfolio website for the Self-Healing Distributed Cache project. Built with vanilla HTML, CSS, and JavaScript — zero dependencies, no build step.

## Quick Start

### View Locally

Simply open `index.html` in your browser:

```bash
# From the repo root
open website/index.html        # macOS
start website/index.html       # Windows
xdg-open website/index.html    # Linux
```

Or use any static file server:

```bash
# Using Python
cd website && python -m http.server 8000

# Using Node.js npx
cd website && npx serve .

# Using Go
cd website && go run -exec "echo" $(echo 'package main; import ("net/http"; "log"); func main() { log.Fatal(http.ListenAndServe(":8080", http.FileServer(http.Dir(".")))) }' > /tmp/serve.go && go run /tmp/serve.go)
```

Then open http://localhost:8000 in your browser.

## Deployment

### GitHub Pages

1. Go to **Repository Settings → Pages**
2. Set **Source** to `Deploy from a branch`
3. Select branch: `main` or `master`
4. Set folder: `/website`
5. Click **Save**

Your site will be available at: `https://yourusername.github.io/self-healing-distributed-cache/`

### Vercel

1. Install Vercel CLI: `npm i -g vercel`
2. From repo root: `vercel --prod`
3. Set root directory to `website`
4. Deploy

### Netlify

1. Drag and drop the `website/` folder to Netlify
2. Or connect your repo and set:
   - Base directory: `website`
   - Publish directory: `website`

### Any Static Host

Upload these files to any web server:

```
index.html
css/style.css
js/main.js
assets/favicon.svg
```

## Customization

### Content

Edit `index.html` to change:
- Section headings and descriptions
- Feature cards
- Stats numbers
- Navigation links
- Footer links

### Styling

Edit `css/style.css` CSS variables at the top:

```css
:root {
    --color-accent: #4a7c59;      /* Change accent color */
    --color-text: #1a1a1a;        /* Change text color */
    --font-heading: 'Inter', ...; /* Change fonts */
}
```

### Interactivity

Edit `js/main.js` to modify:
- Animation timing
- Scroll thresholds
- Counter animation duration

## File Structure

```
website/
├── index.html          # Main page (all sections)
├── css/
│   └── style.css       # Complete stylesheet
├── js/
│   └── main.js         # Interactions & animations
├── assets/
│   └── favicon.svg     # Cache ring icon
└── README.md           # This file
```

## Sections

1. **Header** - Sticky nav with logo, links, GitHub CTA
2. **Hero** - Headline, subtext, terminal with architecture diagram
3. **Features** - 6 feature cards (hashing, replication, failover, etc.)
4. **Architecture** - Visual diagram + 4-step request flow
5. **Stats** - Animated counters (phases, tests, detection time, TTL drift)
6. **Tech Stack** - 6 technology badges
7. **Process** - 4-phase development timeline
8. **API Preview** - Terminal-styled code examples
9. **Docs** - Links to documentation
10. **Footer** - Navigation, resources, links

## Browser Support

- Chrome 80+
- Firefox 75+
- Safari 13+
- Edge 80+

## License

Same as the parent project.
