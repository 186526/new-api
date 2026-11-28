import { Link } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

export function ToolsNavigation() {
  const { t } = useTranslation()
  const linkClass = 'text-primary underline underline-offset-4'

  return (
    <nav aria-label={t('Tools links')} className='flex flex-wrap gap-x-4 gap-y-2 text-sm'>
      <Link className={linkClass} to='/dashboard/$section' params={{ section: 'overview' }}>
        {t('Overview')}
      </Link>
      <Link className={linkClass} to='/usage-logs/$section' params={{ section: 'common' }} search={{ model: 'tools-search' }}>
        {t('Search usage logs')}
      </Link>
      <Link className={linkClass} to='/usage-logs/$section' params={{ section: 'common' }} search={{ model: 'tools-fetch' }}>
        {t('Fetch usage logs')}
      </Link>
      <Link className={linkClass} to='/system-settings/security/$section' params={{ section: 'ssrf' }}>
        {t('SSRF Protection')}
      </Link>
      <Link className={linkClass} to='/channels' search={{ model: 'tools-search' }}>
        {t('Search channels')}
      </Link>
      <Link className={linkClass} to='/channels' search={{ model: 'tools-fetch' }}>
        {t('Fetch channels')}
      </Link>
      <Link className={linkClass} to='/docs' hash='moonshot-service'>
        {t('Client setup')}
      </Link>
    </nav>
  )
}
