import { Prism as SyntaxHighlighter } from 'react-syntax-highlighter'
import { oneDark } from 'react-syntax-highlighter/dist/esm/styles/prism'

interface Props {
  language: string
  code: string
}

export default function MarkdownCodeBlock({ language, code }: Props) {
  return (
    <SyntaxHighlighter
      style={oneDark}
      language={language}
      PreTag="div"
    >
      {code}
    </SyntaxHighlighter>
  )
}
