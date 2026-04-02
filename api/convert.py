"""
Vercel Serverless Function: convert .docx to WeChat HTML
"""

import os, sys, json, tempfile, shutil, zipfile, base64, time
import xml.etree.ElementTree as ET
from datetime import datetime
from http.server import BaseHTTPRequestHandler

# Namespaces
ns = {'w': 'http://schemas.openxmlformats.org/wordprocessingml/2006/main',
      'r': 'http://schemas.openxmlformats.org/officeDocument/2006/relationships'}
wns = 'http://schemas.openxmlformats.org/wordprocessingml/2006/main'
rns_uri = 'http://schemas.openxmlformats.org/officeDocument/2006/relationships'
pkg_ns = 'http://schemas.openxmlformats.org/package/2006/relationships'

BLANK = '<section><span style="font-size: 15px;"><br></span></section>'


def esc(t):
    return t.replace('&', '&amp;').replace('<', '&lt;').replace('>', '&gt;')


def img_src_b64(filename, media_dir):
    path = os.path.join(media_dir, filename)
    if not os.path.exists(path):
        return ''
    with open(path, 'rb') as f:
        data = base64.b64encode(f.read()).decode('ascii')
    mime = 'image/png' if filename.endswith('.png') else 'image/jpeg'
    return f'data:{mime};base64,{data}'


def get_image_width_pct(drawing_elem):
    wp_ns = 'http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing'
    extent = drawing_elem.find(f'.//{{{wp_ns}}}extent')
    if extent is not None:
        cx = int(extent.get('cx', 0))
        if cx > 0:
            pct = min(100, round(cx / 5486400 * 100))
            return f'{pct}%'
    return '100%'


def get_parts(p, rid_to_file, rid_to_url):
    pPr = p.find('w:pPr', ns)
    align = 'left'
    is_list = False
    if pPr is not None:
        jc = pPr.find('w:jc', ns)
        if jc is not None:
            align = jc.get(f'{{{wns}}}val', 'left')
        if pPr.find('w:numPr', ns) is not None:
            is_list = True
    parts = []
    for child in p:
        tag = child.tag.split('}')[-1]
        if tag == 'r':
            rPr = child.find('w:rPr', ns)
            bold = False; sz = ''; color = ''
            if rPr is not None:
                if rPr.find('w:b', ns) is not None: bold = True
                s = rPr.find('w:sz', ns)
                if s is not None: sz = s.get(f'{{{wns}}}val', '')
                c = rPr.find('w:color', ns)
                if c is not None: color = c.get(f'{{{wns}}}val', '')
            all_t = child.findall('w:t', ns)
            run_text = ''.join(t.text for t in all_t if t is not None and t.text)
            drw = child.find('w:drawing', ns)
            if drw is not None:
                blip = drw.find('.//{http://schemas.openxmlformats.org/drawingml/2006/main}blip')
                embed = blip.get(f'{{{rns_uri}}}embed', '') if blip is not None else ''
                width_pct = get_image_width_pct(drw)
                parts.append(dict(type='image', file=rid_to_file.get(embed, ''), width=width_pct))
            elif run_text:
                parts.append(dict(type='text', text=run_text, bold=bold, sz=sz, color=color))
        elif tag == 'hyperlink':
            href_rid = child.get(f'{{{rns_uri}}}id', '')
            href = rid_to_url.get(href_rid, '')
            for r in child.findall('w:r', ns):
                all_t = r.findall('w:t', ns)
                link_text = ''.join(t.text for t in all_t if t is not None and t.text)
                if link_text:
                    parts.append(dict(type='link', text=link_text, href=href))
    return align, parts, is_list


def classify(align, parts, is_list):
    if not parts: return 'blank'
    if any(p['type'] == 'image' for p in parts): return 'image'
    txt = ''.join(p['text'] for p in parts if p['type'] == 'text')
    if txt.strip() == '': return 'blank'
    if txt.strip() == '参考来源': return 'ref_header'
    tp = [p for p in parts if p['type'] == 'text']
    if tp and all(p['bold'] and p['sz'] in ('32', '36') for p in tp): return 'heading'
    if is_list: return 'list'
    if align == 'center': return 'caption'
    return 'text'


def render_inline(parts):
    out = []
    for p in parts:
        if p['type'] == 'text':
            t = esc(p['text'])
            out.append(f'<strong>{t}</strong>' if p['bold'] else t)
        elif p['type'] == 'link':
            t = esc(p['text'])
            h = p['href'].replace('&', '&amp;')
            out.append(f'<a style="color: rgb(0, 112, 192); text-decoration: none;" href="{h}">{t}</a>')
    return ''.join(out)


def run_layout(docx_bytes):
    t0 = time.time()

    # Write to temp file & unpack
    tmp = tempfile.mkdtemp()
    docx_path = os.path.join(tmp, 'input.docx')
    with open(docx_path, 'wb') as f:
        f.write(docx_bytes)

    unpacked = os.path.join(tmp, 'unpacked')
    with zipfile.ZipFile(docx_path, 'r') as z:
        z.extractall(unpacked)
    media_dir = os.path.join(unpacked, 'word', 'media')

    # Parse rels
    rels_path = os.path.join(unpacked, 'word', '_rels', 'document.xml.rels')
    rels_tree = ET.parse(rels_path)
    rid_to_file = {}
    rid_to_url = {}
    for rel in rels_tree.getroot().findall(f'{{{pkg_ns}}}Relationship'):
        rid = rel.get('Id')
        target = rel.get('Target')
        rtype = rel.get('Type', '')
        if 'image' in rtype:
            rid_to_file[rid] = target.replace('media/', '')
        elif 'hyperlink' in rtype:
            rid_to_url[rid] = target.replace('&amp;', '&')

    # Parse document
    tree = ET.parse(os.path.join(unpacked, 'word', 'document.xml'))
    body = tree.getroot().find('w:body', ns)
    all_paras = list(body.findall('w:p', ns))

    start_idx = 0
    for i, p in enumerate(all_paras):
        text = ''.join(t.text for t in p.findall('.//w:t', ns) if t.text)
        if text.strip() == '正文':
            start_idx = i + 1
            break

    # Collect elements
    elements = []
    img_n = 0
    list_counter = 0
    for i in range(start_idx, len(all_paras)):
        align, parts, is_list = get_parts(all_paras[i], rid_to_file, rid_to_url)
        kind = classify(align, parts, is_list)
        if kind == 'blank':
            list_counter = 0
            continue
        if kind == 'image':
            for p in parts:
                if p['type'] == 'image':
                    img_n += 1
                    elements.append(dict(kind='image', file=p['file'], width=p.get('width', '100%')))
        elif kind == 'heading':
            txt = ''.join(p['text'] for p in parts if p['type'] == 'text')
            elements.append(dict(kind='heading', text=txt))
        elif kind == 'caption':
            elements.append(dict(kind='caption', html=render_inline(parts)))
        elif kind == 'ref_header':
            elements.append(dict(kind='ref_header'))
        elif kind == 'list':
            list_counter += 1
            elements.append(dict(kind='list', html=render_inline(parts), num=list_counter))
        elif kind == 'text':
            list_counter = 0
            elements.append(dict(kind='text', html=render_inline(parts)))

    # Generate HTML
    lines = []
    in_ref = False

    def prev_kind():
        for l in reversed(lines):
            if l != BLANK:
                if 'font-size: 18px; font-weight: bold' in l: return 'heading'
                if '<img ' in l: return 'image'
                if 'font-size: 12px; color: #888888' in l: return 'caption'
                return 'text'
        return None

    def ensure_blank():
        if lines and lines[-1] != BLANK:
            lines.append(BLANK)

    for elem in elements:
        k = elem['kind']
        pk = prev_kind()
        if k == 'ref_header':
            in_ref = True
            ensure_blank()
            lines.append('<section style="margin-left: 8px; margin-right: 8px; line-height: 1.75em;"><span style="font-size: 15px; color: #888888;">参考来源：</span></section>')
            continue
        if in_ref:
            lines.append(f'<section style="margin-left: 8px; margin-right: 8px; line-height: 1.75em;"><span style="font-size: 15px; color: #888888;">{elem.get("html","")}</span></section>')
            continue
        if k == 'heading':
            ensure_blank()
            lines.append(f'<section style="margin-left: 8px; margin-right: 8px; line-height: 1.75em;"><span style="font-size: 18px; font-weight: bold; color: #544545;">{esc(elem["text"])}</span></section>')
            lines.append(BLANK)
            continue
        if k == 'image':
            if pk not in ('image', 'caption', None):
                ensure_blank()
            src = img_src_b64(elem['file'], media_dir)
            w = elem.get('width', '100%')
            lines.append(f'<section style="text-align: center; margin-left: 8px; margin-right: 8px;"><img src="{src}" style="width: {w}; display: block; margin: 0 auto;" /></section>')
            continue
        if k == 'caption':
            lines.append(f'<section style="text-align: center; margin-left: 8px; margin-right: 8px;"><span style="font-size: 12px; color: #888888;">{elem["html"]}</span></section>')
            continue
        if k == 'list':
            if pk in ('text',) and elem['num'] == 1:
                ensure_blank()
            lines.append(f'<section style="margin-left: 8px; margin-right: 8px; line-height: 1.75em;"><span style="font-size: 15px; color: #544545;">{elem["num"]}、{elem["html"]}</span></section>')
            continue
        if k == 'text':
            if pk in ('text', 'caption', 'image'):
                ensure_blank()
            lines.append(f'<section style="margin-left: 8px; margin-right: 8px; line-height: 1.75em;"><span style="font-size: 15px; color: #544545;">{elem["html"]}</span></section>')

    # Dedup blanks
    final = []
    for l in lines:
        if l == BLANK and final and final[-1] == BLANK:
            continue
        final.append(l)

    # Load footer
    footer_html = ''
    footer_path = os.path.join(os.path.dirname(os.path.dirname(__file__)), 'data', 'fbif-footer-template.html')
    try:
        with open(footer_path, 'r', encoding='utf-8', errors='replace') as f:
            footer_html = f.read()
    except:
        pass

    # Build content
    author_line = '<section style="margin-left: 8px; margin-right: 8px; line-height: 1.75em;"><span style="font-size: 15px; color: rgb(0, 112, 192);">作者：Mote莫特</span></section>'
    content_html = author_line + '\n' + BLANK + '\n' + '\n'.join(final) + '\n' + footer_html

    # Cleanup
    shutil.rmtree(tmp, ignore_errors=True)

    elapsed = time.time() - t0
    heading_count = sum(1 for e in elements if e['kind'] == 'heading')
    stats = f"段落: {len(final)} | 图片: {img_n} | 标题: {heading_count} | 耗时: {elapsed:.1f}s"

    return content_html, stats


def parse_multipart(body, content_type):
    """Parse multipart/form-data to extract the uploaded file."""
    boundary = content_type.split('boundary=')[-1].encode()
    parts = body.split(b'--' + boundary)
    for part in parts:
        if b'filename="' not in part:
            continue
        # Extract filename
        header_end = part.find(b'\r\n\r\n')
        if header_end == -1:
            continue
        header = part[:header_end].decode('utf-8', errors='replace')
        file_data = part[header_end + 4:]
        # Strip trailing \r\n
        if file_data.endswith(b'\r\n'):
            file_data = file_data[:-2]
        if file_data.endswith(b'--'):
            file_data = file_data[:-2]
        if file_data.endswith(b'\r\n'):
            file_data = file_data[:-2]
        # Extract filename
        fn_start = header.find('filename="') + 10
        fn_end = header.find('"', fn_start)
        filename = header[fn_start:fn_end]
        return filename, file_data
    return None, None


class handler(BaseHTTPRequestHandler):
    def do_POST(self):
        content_type = self.headers.get('Content-Type', '')
        content_length = int(self.headers.get('Content-Length', 0))
        body = self.rfile.read(content_length)

        if 'multipart/form-data' not in content_type:
            self.send_response(400)
            self.send_header('Content-Type', 'application/json')
            self.end_headers()
            self.wfile.write(json.dumps({'error': '请上传文件'}).encode())
            return

        filename, file_data = parse_multipart(body, content_type)
        if not filename or not file_data:
            self.send_response(400)
            self.send_header('Content-Type', 'application/json')
            self.end_headers()
            self.wfile.write(json.dumps({'error': '未收到文件'}).encode())
            return

        if not filename.endswith('.docx'):
            self.send_response(400)
            self.send_header('Content-Type', 'application/json')
            self.end_headers()
            self.wfile.write(json.dumps({'error': '请上传 .docx 文件'}).encode())
            return

        try:
            title = os.path.splitext(filename)[0]
            content_html, stats = run_layout(file_data)
            result = json.dumps({'title': title, 'content': content_html, 'stats': stats})
            self.send_response(200)
            self.send_header('Content-Type', 'application/json')
            self.end_headers()
            self.wfile.write(result.encode())
        except Exception as e:
            self.send_response(500)
            self.send_header('Content-Type', 'application/json')
            self.end_headers()
            self.wfile.write(json.dumps({'error': f'排版失败: {str(e)}'}).encode())
