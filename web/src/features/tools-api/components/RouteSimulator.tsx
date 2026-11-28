import { useMutation, useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useId, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Field, FieldDescription, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from '@/components/ui/sheet'
import {
  Table,
  TableBody,
  TableCaption,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { CHANNEL_TYPE_OPTIONS } from '@/features/channels/constants'
import { PlaygroundTools } from '@/features/playground/components/playground-tools'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { useAuthStore } from '@/stores/auth-store'

import {
  getSimulatorChannels,
  simulateToolRoute,
  type SimulationChannel,
} from '../lib/admin'
import {
  TOOL_CONTRACTS,
  TOOL_ENDPOINTS,
  type ToolContract,
} from '../lib/group-policies'

function SimulationChannels(props: { channels: SimulationChannel[] }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const captionId = useId()

  return (
    <div
      role='region'
      aria-labelledby={captionId}
      tabIndex={0}
      className='max-h-72 min-w-0 overflow-auto'
    >
      <Table withContainer={false} className='caption-top'>
        <TableCaption id={captionId}>{t('Channels')}</TableCaption>
        <TableHeader className='bg-background sticky top-0'>
          <TableRow>
            <TableHead scope='col'>{t('Channel')}</TableHead>
            <TableHead scope='col'>{t('Group')}</TableHead>
            <TableHead scope='col'>{t('Model')}</TableHead>
            <TableHead scope='col'>{t('Priority')}</TableHead>
            <TableHead scope='col'>{t('Weight')}</TableHead>
            <TableHead scope='col'>{t('Candidate order')}</TableHead>
            <TableHead scope='col'>{t('Status')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {props.channels.map((channel) => (
            <TableRow key={`${channel.group}-${channel.channel_id}`}>
              <TableHead scope='row'>
                {channel.channel_name} (#{channel.channel_id})
              </TableHead>
              <TableCell>{channel.group}</TableCell>
              <TableCell>{channel.matched_model}</TableCell>
              <TableCell>{formatNumber(channel.priority, locale)}</TableCell>
              <TableCell>{formatNumber(channel.weight, locale)}</TableCell>
              <TableCell>{channel.providers.map(({ provider }) => provider).join(' → ')}</TableCell>
              <TableCell>
                {channel.first_attempt ? t('First attempt') : t('Eligible')}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

export function RouteSimulator(props: {
  groups: string[]
  selectedGroup: string
  fixedChannelId?: number
}) {
  const { t } = useTranslation()
  const id = useId()
  const [contract, setContract] = useState<ToolContract>('moonshot/search')
  const [group, setGroup] = useState(props.selectedGroup)
  const [open, setOpen] = useState(false)
  const [showLiveTest, setShowLiveTest] = useState(false)
  const userId = useAuthStore((state) => state.auth.user?.id)
  const channels = useQuery({
    queryKey: ['tools-api', 'simulation-channels'],
    queryFn: ({ signal }) => getSimulatorChannels(signal),
    enabled: open && props.fixedChannelId === undefined,
    meta: { errorToast: false },
  })
  const channelType = (type: number) =>
    CHANNEL_TYPE_OPTIONS.find((option) => option.value === type)?.label ?? t('Unknown')
  const simulation = useMutation({
    mutationFn: simulateToolRoute,
    onError: (error) =>
      handleServerError(error, t('Tool route simulation failed')),
  })
  const result = simulation.data
  const labels = useMemo(
    () => ({
      routeStatus: new Map([
        [200, t('Allowed')],
        [400, t('Price not configured')],
        [403, t('Tool disabled for group')],
        [503, t('No available route')],
      ]),
      ruleSource: new Map([
        ['global_candidate_order', t('Global provider order')],
        ['group_only', t('Only providers listed for the group')],
        ['group_prefer', t('Providers preferred by the group')],
        ['builtin_fallback', t('Built-in tool fallback')],
      ]),
      pricingStatus: new Map([
        ['missing', t('Price not configured')],
        ['expression', t('Billing expression')],
        ['fixed_price', t('Fixed price')],
        ['model_ratio', t('Model ratio')],
      ]),
      candidateOrigin: new Map([
        ['operator_binding', t('Channel provider override')],
        ['native_channel', t('Native channel')],
        ['channel_upstream', t('Channel upstream')],
        ['global_credential', t('Global credential')],
        ['local', t('Local fetch')],
      ]),
      stepReason: new Map([
        ['group_disabled', t('Tool disabled for group')],
        ['no_eligible_channel', t('No eligible channel')],
        ['no_executable_provider', t('No usable provider')],
        ['selected', t('Channel selected')],
        ['missing_price', t('Price not configured')],
        ['builtin_fallback', t('Built-in tool fallback')],
        ['group_not_selectable', t('Group is not selectable')],
        ['weighted_sample', t('Sampled channel')],
      ]),
      eligibilityReason: new Map([
        ['eligible', t('Eligible')],
        ['channel_disabled', t('Channel disabled')],
        ['group_disabled', t('Tool disabled for group')],
        ['ability_not_enabled', t('Model is not enabled for this channel and group')],
        ['unsupported_contract', t('Endpoint not supported')],
        ['provider_not_allowed', t('Provider not allowed by group policy')],
        ['missing_credential', t('Missing provider credential')],
        ['missing_model_mapping', t('Missing model mapping')],
        ['missing_request_preparation', t('Provider cannot prepare this request')],
        ['builtin_fallback_disabled', t('Built-in fallback is disabled')],
      ]),
      unknown: t('Unknown'),
    }),
    [t]
  )

  return (
    <Sheet
      onOpenChange={(open) => {
        setOpen(open)
        setShowLiveTest(false)
        if (open) {
          setGroup(props.selectedGroup)
          simulation.reset()
        }
      }}
    >
      <SheetTrigger render={<Button type='button' variant='outline' />}>
        {t('Simulate tool route')}
      </SheetTrigger>
      <SheetContent className='overflow-y-auto'>
        <SheetHeader>
          <SheetTitle id={`${id}-title`}>{t('Simulate tool route')}</SheetTitle>
          <SheetDescription>
            {t(
              'Preview saved routing rules without calling a provider or charging a key.'
            )}
          </SheetDescription>
        </SheetHeader>
        <div className='flex flex-col gap-4 px-4 pb-4'>
          <form
            aria-labelledby={`${id}-title`}
            method='post'
            onSubmit={(event) => {
              event.preventDefault()
              event.stopPropagation()
              const fields = new FormData(event.currentTarget)
              const channel = String(fields.get('channel') ?? '').trim()
              const model = String(fields.get('model') ?? '').trim()
              const userGroup = String(fields.get('userGroup') ?? '')
              simulation.mutate({
                group,
                contract,
                channel_id: props.fixedChannelId ?? (channel ? Number(channel) : undefined),
                model: model || undefined,
                user_group: userGroup || undefined,
              })
            }}
          >
            <FieldGroup>
              <Field>
                <FieldLabel htmlFor={`${id}-group`}>{t('Group')}</FieldLabel>
                <NativeSelect
                  id={`${id}-group`}
                  name='group'
                  value={group}
                  onChange={(event) => setGroup(event.target.value)}
                >
                  {[...new Set([...props.groups, 'auto'])].map((name) => (
                    <NativeSelectOption key={name} value={name}>
                      {name}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              </Field>
              {group === 'auto' && (
                <Field>
                  <FieldLabel htmlFor={`${id}-user-group`}>
                    {t('User group')}
                  </FieldLabel>
                  <NativeSelect id={`${id}-user-group`} name='userGroup'>
                    <NativeSelectOption value=''>
                      {t('Use default group')}
                    </NativeSelectOption>
                    {props.groups
                      .filter((name) => name !== 'auto')
                      .map((name) => (
                        <NativeSelectOption key={name} value={name}>
                          {name}
                        </NativeSelectOption>
                      ))}
                  </NativeSelect>
                </Field>
              )}
              <Field>
                <FieldLabel htmlFor={`${id}-contract`}>
                  {t('Endpoint')}
                </FieldLabel>
                <NativeSelect
                  id={`${id}-contract`}
                  name='contract'
                  value={contract}
                  onChange={(event) =>
                    setContract(event.target.value as ToolContract)
                  }
                >
                  {TOOL_CONTRACTS.map((name) => (
                    <NativeSelectOption key={name} value={name}>
                      {TOOL_ENDPOINTS[name]}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              </Field>
              {contract === 'alpha/search' && (
                <Field>
                  <FieldLabel htmlFor={`${id}-model`}>{t('Model')}</FieldLabel>
                  <Input
                    id={`${id}-model`}
                    name='model'
                    required
                    maxLength={255}
                  />
                </Field>
              )}
              {props.fixedChannelId === undefined ? (
                <Field>
                  <FieldLabel htmlFor={`${id}-channel`}>
                    {t('Channel (optional)')}
                  </FieldLabel>
                  <NativeSelect id={`${id}-channel`} name='channel' aria-describedby={`${id}-channel-help`}>
                    <NativeSelectOption value=''>{t('Select channel automatically')}</NativeSelectOption>
                    {channels.data?.map((channel) => (
                      <NativeSelectOption key={channel.id} value={channel.id}>
                        {`${channel.name} · ${channelType(channel.type)} (#${channel.id})`}
                      </NativeSelectOption>
                    ))}
                  </NativeSelect>
                  <FieldDescription id={`${id}-channel-help`}>
                    {channels.isError
                      ? t('Channel list is unavailable.')
                      : t('Leave blank to preview automatic routing.')}
                  </FieldDescription>
                  {channels.isError && (
                    <Button type='button' variant='outline' onClick={() => void channels.refetch()}>
                      {t('Retry')}
                    </Button>
                  )}
                </Field>
              ) : (
                <p className='text-sm'>{t('Channel')}: #{props.fixedChannelId}</p>
              )}
              <Button type='submit' disabled={simulation.isPending}>
                {t('Run simulation')}
              </Button>
            </FieldGroup>
          </form>
          <p role='status' className='text-muted-foreground text-sm'>
            {simulation.isPending && t('Simulating…')}
            {simulation.isError && t('Tool route simulation failed')}
            {result?.preflight && <>{t('Read-only preflight')} · </>}
            {result?.source === 'builtin_fallback'
              ? t('Route uses built-in fallback without a channel')
              : result &&
                t('Routing status: {{status}}', {
                  status: `${labels.routeStatus.get(result.status) ?? labels.unknown} (${result.status})`,
                })}
          </p>
          {result && (
            <div className='flex flex-col gap-2 text-sm'>
              <p>
                {t('Group')}: {result.group ?? simulation.variables?.group}
              </p>
              {result.authorization_verified === false && (
                <p>{t('Runtime authorization was not verified.')}</p>
              )}
              {result.channel_id !== undefined && (
                <p>
                  {result.channel_is_sample ? t('Sampled channel') : t('Channel')}: {result.channel_name} (#{result.channel_id})
                  {result.channel_type != null && ` · ${channelType(result.channel_type)}`}
                </p>
              )}
              {!!result.channels?.length && (
                <SimulationChannels channels={result.channels} />
              )}
              {!!result.eligibility?.length && (
                <div>
                  <p>{t('Eligibility details')}</p>
                  <ul className='list-disc ps-5'>
                    {result.eligibility.map((entry) => (
                      <li key={JSON.stringify([entry.group, entry.channel_id, entry.provider, entry.origin, entry.reason])}>
                        {entry.group}
                        {entry.channel_id != null && ` · #${entry.channel_id}`}
                        {entry.provider && ` · ${entry.provider}`}
                        {entry.origin && ` · ${labels.candidateOrigin.get(entry.origin) ?? labels.unknown}`}
                        {': '}{labels.eligibilityReason.get(entry.reason) ?? labels.unknown}
                      </li>
                    ))}
                  </ul>
                </div>
              )}
              {result.weighted_selection && (
                <p>{t('Weighted selection may choose another channel.')}</p>
              )}
              {result.source && (
                <p>
                  {t('Rule source')}:{' '}
                  <span>
                    {labels.ruleSource.get(result.source) ?? labels.unknown}
                  </span>
                </p>
              )}
              <p>
                {t('Billing model')}: <code>{result.billing_model}</code>
              </p>
              <p>
                {t('Pricing status')}:{' '}
                <span>
                  {labels.pricingStatus.get(result.pricing_status) ??
                    labels.unknown}
                </span>
              </p>
              {result.billing_expression && (
                <code className='wrap-anywhere'>
                  {result.billing_expression}
                </code>
              )}
              {result.legacy_price != null && (
                <p>
                  {t('Model price')}: {result.legacy_price}
                </p>
              )}
              {result.legacy_model_ratio != null && (
                <p>
                  {t('Model ratio')}: {result.legacy_model_ratio}
                </p>
              )}
              {result.tool_price_per_thousand != null && (
                <p>
                  {t('Tool price per 1,000 calls')}:{' '}
                  {result.tool_price_per_thousand}
                </p>
              )}
              {result.group_ratio != null && (
                <p>
                  {t('Group ratio')}: {result.group_ratio}
                </p>
              )}
              <p>{t('Candidate order')}</p>
              {result.candidates?.length ? (
                <ol className='list-decimal ps-5'>
                  {result.candidates.map((entry) => (
                    <li key={`${entry.origin}-${entry.provider}`}>
                      {entry.provider} —{' '}
                      {labels.candidateOrigin.get(entry.origin) ??
                        labels.unknown}
                      {entry.native && ` · ${t('Native')}`}
                    </li>
                  ))}
                </ol>
              ) : (
                <p>{t('No candidates')}</p>
              )}
              {!!result.steps?.length && (
                <ul className='list-disc ps-5'>
                  {result.steps.map((step) => (
                    <li key={`${step.group}-${step.reason}`}>
                      {step.group}:{' '}
                      {labels.stepReason.get(step.reason) ?? labels.unknown}
                    </li>
                  ))}
                </ul>
              )}
              <Link
                to='/playground'
                search={{
                  mode: 'tools',
                  endpoint: simulation.variables?.contract,
                  group: simulation.variables?.group,
                  model:
                    simulation.variables?.contract === 'alpha/search'
                      ? simulation.variables.model
                      : undefined,
                }}
                className='text-primary self-start underline'
              >
                {t('Open Playground')}
              </Link>
            </div>
          )}
          {props.fixedChannelId !== undefined && userId !== undefined && (
            <>
              <Button
                type='button'
                variant='outline'
                aria-expanded={showLiveTest}
                aria-controls={`${id}-live-test`}
                onClick={() => setShowLiveTest((visible) => !visible)}
              >
                {t('Test saved channel')}
              </Button>
              <div id={`${id}-live-test`} hidden={!showLiveTest}>
                {showLiveTest && (
                  <PlaygroundTools
                    userId={userId}
                    endpoint={simulation.variables?.contract ?? contract}
                    group={simulation.variables?.group ?? group}
                    model={simulation.variables?.model}
                    fixedChannelId={props.fixedChannelId}
                  />
                )}
              </div>
            </>
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}
