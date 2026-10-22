// Renders a JSON array of {tex, display} to MathJax SVG strings on stdout.
// Errors are returned as null so the caller can fall back to raw TeX.
const {mathjax} = require('mathjax-full/js/mathjax.js');
const {TeX} = require('mathjax-full/js/input/tex.js');
const {AllPackages} = require('mathjax-full/js/input/tex/AllPackages.js');
const {SVG} = require('mathjax-full/js/output/svg.js');
const {liteAdaptor} = require('mathjax-full/js/adaptors/liteAdaptor.js');
const {RegisterHTMLHandler} = require('mathjax-full/js/handlers/html.js');

const adaptor = liteAdaptor();
RegisterHTMLHandler(adaptor);
const tex = new TeX({packages: ['base', 'ams', 'mathtools', 'newcommand', 'configmacros', 'autoload']});
const svg = new SVG({fontCache: 'local'});
const doc = mathjax.document('', {InputJax: tex, OutputJax: svg});

let input = '';
process.stdin.setEncoding('utf8');
process.stdin.on('data', (chunk) => { input += chunk; });
process.stdin.on('end', () => {
  let items;
  try {
    items = JSON.parse(input);
  } catch (err) {
    process.stderr.write('invalid input json\n');
    process.exit(2);
  }
  if (!Array.isArray(items) || items.length > 500) {
    process.stderr.write('invalid input\n');
    process.exit(2);
  }
  const output = items.map((item) => {
    try {
      if (typeof item.tex !== 'string' || item.tex.length === 0 || item.tex.length > 20000) {
        return null;
      }
      const html = adaptor.innerHTML(doc.convert(item.tex, {display: !!item.display}));
      if (!html || html.indexOf('<svg') < 0 || html.indexOf('merror') >= 0) {
        return null;
      }
      if (/<script|<foreignObject|javascript:|\son[a-z]+\s*=/i.test(html)) {
        return null;
      }
      return html;
    } catch (err) {
      return null;
    }
  });
  process.stdout.write(JSON.stringify(output));
});
